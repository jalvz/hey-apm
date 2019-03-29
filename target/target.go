package target

import (
	"bytes"
	"compress/gzip"
	"container/ring"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/hey-apm/compose"
	"github.com/elastic/hey-apm/requester"
)

const defaultUserAgent = "hey-apm/1.0"

type Config struct {
	NumAgents      int
	Throttle       float64
	Pause          time.Duration
	MaxRequests    int
	RequestTimeout time.Duration
	RunTimeout     time.Duration
	Endpoint       string
	SecretToken    string
	Stream         bool
	*BodyConfig
	DisableCompression, DisableKeepAlives, DisableRedirects bool
	http.Header
}

type BodyConfig struct {
	NumErrors, NumTransactions, NumSpans, NumFrames int
}

type Target struct {
	URLs   *ring.Ring
	Method string
	Body   []byte
	Config *Config
}

func defaultCfg() *Config {
	return &Config{
		MaxRequests:    math.MaxInt32,
		RequestTimeout: 10 * time.Second,
		Endpoint:       "/intake/v2/events",
		BodyConfig:     &BodyConfig{},
		Header:         make(http.Header),
	}
}

func buildBody(b *BodyConfig) []byte {
	return compose.Compose(b.NumErrors, b.NumTransactions, b.NumSpans, b.NumFrames)
}

func NewTargetFromConfig(url, method string, cfg *Config) *Target {
	if cfg == nil {
		cfg = defaultCfg()
	}
	body := buildBody(cfg.BodyConfig)
	ring := ring.New(1)
	ring.Value = strings.TrimSuffix(url, "/") + cfg.Endpoint
	return &Target{Config: cfg, Body: body, URLs: ring, Method: method}
}

func NewTargetFromOptions(urls []string, opts ...OptionFunc) (*Target, error) {
	cfg := defaultCfg()
	var err error
	for _, opt := range opts {
		err = with(cfg, opt, err)
	}
	body := buildBody(cfg.BodyConfig)
	ring := ring.New(len(urls))
	for _, url := range urls {
		ring.Value = strings.TrimSuffix(url, "/") + cfg.Endpoint
		ring = ring.Next()
	}
	return &Target{Config: cfg, Body: body, URLs: ring, Method: "POST"}, err
}

type OptionFunc func(*Config) error

func with(c *Config, f OptionFunc, err error) error {
	if err != nil {
		return err
	}
	return f(c)
}

func SecretToken(s string) OptionFunc {
	return func(c *Config) error {
		c.SecretToken = s
		return nil
	}
}

func RunTimeout(s string) OptionFunc {
	return func(c *Config) error {
		var err error
		c.RunTimeout, err = time.ParseDuration(s)
		return err
	}
}

func RequestTimeout(d time.Duration) OptionFunc {
	return func(c *Config) error {
		c.RequestTimeout = d
		return nil
	}
}

func NumAgents(i int) OptionFunc {
	return func(c *Config) error {
		c.NumAgents = i
		return nil
	}
}

func Throttle(i int) OptionFunc {
	return func(c *Config) error {
		c.Throttle = float64(i)
		return nil
	}
}

func Pause(d time.Duration) OptionFunc {
	return func(c *Config) error {
		c.Pause = d
		return nil
	}
}

func Stream(b bool) OptionFunc {
	return func(c *Config) error {
		c.Stream = b
		return nil
	}
}

func NumErrors(i int) OptionFunc {
	return func(c *Config) error {
		c.NumErrors = i
		return nil
	}
}

func NumTransactions(s string) OptionFunc {
	return func(c *Config) error {
		var err error
		c.NumTransactions, err = strconv.Atoi(s)
		return err
	}
}

func NumSpans(s string) OptionFunc {
	return func(c *Config) error {
		var err error
		c.NumSpans, err = strconv.Atoi(s)
		return err
	}
}

func NumFrames(s string) OptionFunc {
	return func(c *Config) error {
		var err error
		c.NumFrames, err = strconv.Atoi(s)
		return err
	}
}

func (t *Target) Size() int64 {
	return int64(len(t.Body))
}

// Returns a runnable that simulates APM agents sending requests to APM Server with the `target` configuration
// Mutates t.Body (for compression) and t.Headers
func (t *Target) GetWork(w io.Writer) *requester.Work {

	// Use the defaultUserAgent unless the Header contains one, which may be blank to not send the header.
	if _, ok := t.Config.Header["User-Agent"]; !ok {
		t.Config.Header.Add("User-Agent", defaultUserAgent)
	}

	t.Config.Header.Add("Authorization", fmt.Sprintf("Bearer %s", t.Config.SecretToken))

	if len(t.Body) > 0 {
		t.Config.Header.Add("Content-Type", "application/x-ndjson")
	}

	if !t.Config.DisableCompression {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write([]byte(t.Body)); err != nil {
			panic(err)
		}
		if err := gz.Close(); err != nil {
			panic(err)
		}
		t.Body = b.Bytes()
		t.Config.Header.Add("Content-Encoding", "gzip")
	}

	var workReq requester.Req
	if t.Config.Stream {
		workReq = &requester.StreamReq{
			Method:        t.Method,
			URLs:          t.URLs,
			Header:        t.Config.Header,
			Timeout:       t.Config.RequestTimeout,
			RunTimeout:    t.Config.RunTimeout,
			EPS:           t.Config.Throttle,
			PauseDuration: t.Config.Pause,
			RequestBody:   t.Body,
		}
	} else {
		workReq = &requester.SimpleReq{
			Request:     request(t.Method, t.URLs.Value.(string), t.Config.Header, t.Body),
			RequestBody: t.Body,
			URLs:        t.URLs,
			Timeout:     int(t.Config.RequestTimeout.Seconds()),
			QPS:         t.Config.Throttle,
		}
	}

	return &requester.Work{
		Req:                workReq,
		N:                  t.Config.MaxRequests,
		C:                  t.Config.NumAgents,
		DisableCompression: t.Config.DisableCompression,
		DisableKeepAlives:  t.Config.DisableKeepAlives,
		DisableRedirects:   t.Config.DisableRedirects,
		H2:                 false,
		ProxyAddr:          nil,
		Writer:             w,
	}
}

func request(method, url string, headers http.Header, body []byte) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err)
	}
	for header, values := range headers {
		for _, v := range values {
			req.Header.Add(header, v)
		}
	}
	req.ContentLength = int64(len(body))
	return req
}
