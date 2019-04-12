package work

import (
	"context"
	errs "errors"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/heptio/workgroup"

	"github.com/elastic/hey-apm/tracer"

	"go.elastic.co/apm"
	"go.elastic.co/apm/stacktrace"
)

const (
	Transaction = iota
	Error       = iota
)

type Workload struct {
	EventType int
	Limit     int
	Frequency time.Duration
	// for transactions MaxStructs is maximum number of spans per transaction
	// for errors it is minimum number of frames per error
	MaxStructs int
	MinStructs int
}

type Report struct {
	Stats apm.TracerStats
	Start time.Time
	// timestamp after run, before flush
	Stop time.Time
	// timestamp after flush
	End time.Time
}

func Run(t *tracer.Tracer, runTimeout time.Duration, workload []Workload) (Report, error) {
	var w workgroup.Group

	for _, wk := range workload {

		var g func(<-chan interface{}, *tracer.Tracer, int, int, int) generator

		switch wk.EventType {
		case Transaction:
			g = transactions
		case Error:
			g = errors
		}

		if wk.Limit > 0 {
			w.Add(g(throttle(wk.Frequency), t, wk.Limit, wk.MinStructs, wk.MaxStructs))
		}
	}
	if runTimeout > 0 {
		w.Add(timeout(runTimeout))
	}
	w.Add(handleSignals())

	report := Report{Start: time.Now()}
	err := w.Run()
	report.Stop = time.Now()
	t.FlushAll()
	report.End = time.Now()
	report.Stats = t.Stats()
	return report, err
}

// throttle converts a time ticker to a channel of things
func throttle(d time.Duration) chan interface{} {
	throttle := make(chan interface{})
	go func() {
		for range time.NewTicker(d).C {
			throttle <- struct{}{}
		}
	}()
	return throttle
}

type generator func(<-chan struct{}) error

func transactions(throttle <-chan interface{}, tracer *tracer.Tracer, limit, spanMin, spanMax int) generator {
	generateSpan := func(ctx context.Context) {
		span, ctx := apm.StartSpan(ctx, "I'm a span", "gen.era.ted")
		span.End()
	}
	return func(done <-chan struct{}) error {
		sent := 0
		for sent < limit {
			select {
			case <-done:
				return nil
			case <-throttle:
			}

			tx := tracer.StartTransaction("generated", "gen")
			ctx := apm.ContextWithTransaction(context.Background(), tx)
			var wg sync.WaitGroup
			spanCount := rand.Intn(spanMax-spanMin+1) + spanMin
			for i := 0; i < spanCount; i++ {
				wg.Add(1)
				go func() {
					generateSpan(ctx)
					wg.Done()
				}()
			}
			wg.Wait()
			tx.Context.SetTag("spans", strconv.Itoa(spanCount))
			tx.End()
			sent++
		}
		return nil
	}

}

type generatedErr struct {
	frames int
}

func (e *generatedErr) Error() string {
	plural := "s"
	if e.frames == 1 {
		plural = ""
	}
	return fmt.Sprintf("Generated error with %d stacktrace frame%s", e.frames, plural)
}

func (e *generatedErr) StackTrace() []stacktrace.Frame {
	st := make([]stacktrace.Frame, e.frames)
	for i := 0; i < e.frames; i++ {
		st[i] = stacktrace.Frame{
			File:     "fake.go",
			Function: "oops",
			Line:     i + 100,
		}
	}
	return st
}

func errors(throttle <-chan interface{}, tracer *tracer.Tracer, limit, framesMin, framesMax int) generator {
	return func(done <-chan struct{}) error {
		sent := 0
		for sent < limit {
			select {
			case <-done:
				return nil
			case <-throttle:
			}
			tracer.NewError(&generatedErr{frames: rand.Intn(framesMax-framesMin+1) + framesMin}).Send()
			sent++
		}
		return nil
	}
}

func timeout(d time.Duration) generator {
	return func(done <-chan struct{}) error {
		select {
		case <-done:
			return nil
		case <-time.After(d):
			return nil // time expired
		}
	}
}

func handleSignals() generator {
	return func(done <-chan struct{}) error {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)

		select {
		case <-done:
			return nil
		case sig := <-c:
			return errs.New(sig.String())
		}
	}
}
