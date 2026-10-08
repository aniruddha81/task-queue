// Package worker runs job handlers against dispatch.
//
//	w := worker.New(worker.Config{Dispatchers: []string{"http://dispatch:8081"}, Cloud: "aws", Queues: []string{"default"}})
//	w.Handle("email.send", func(ctx context.Context, job worker.Job) error { ... })
//	w.Run(ctx) // returns after ctx ends and in-flight jobs finish
//
// A job may run more than once (a worker can crash after the work but before reporting it).
// Handlers make their effect happen once by passing job.EffectKey to the destination.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"uuid"

	"connectrpc.com/connect"

	workerv1 "github.com/aniruddha81/task-queue/gen/taskqueue/worker/v1"
	"github.com/aniruddha81/task-queue/gen/taskqueue/worker/v1/workerv1connect"
)

// Job is one attempt at a job.
type Job struct {
	ID, Queue, Type string
	Payload         []byte // JSON
	Attempt         int
	EffectKey       string // stable across retries; destinations deduplicate on it
}

// Handler runs a job. Its context ends when the lease is lost, the deadline passes, or
// the job is cancelled; the handler should stop promptly then.
type Handler func(ctx context.Context, job Job) error

// Permanent wraps an error that retrying won't fix: the job goes straight to dead letter.
func Permanent(err error) error { return permanent{err} }

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

var (
	errLeaseLost = errors.New("lease lost")
	errCancelled = errors.New("job cancelled")
)

type Config struct {
	Dispatchers    []string // base URLs, nearest first; the worker moves to the next on errors
	Cloud          string   // "aws" or "azure"
	Queues         []string
	Name, Version  string
	Concurrency    int           // jobs at once; default 4
	HeartbeatEvery time.Duration // default 10s (a third of dispatch's 30 s lease)
	Log            *slog.Logger
	HTTPClient     *http.Client // default: plain-text HTTP/2
}

type Worker struct {
	cfg      Config
	node     uuid.UUID
	handlers map[string]Handler
	clients  []workerv1connect.WorkerServiceClient

	mu     sync.Mutex
	next   int                 // index of the dispatcher in use
	active map[string]*running // by job ID
}

type running struct {
	lease  *workerv1.Lease
	cancel context.CancelCauseFunc
}

func New(cfg Config) *Worker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.HeartbeatEvery <= 0 {
		cfg.HeartbeatEvery = 10 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HTTPClient == nil {
		var p http.Protocols
		p.SetUnencryptedHTTP2(true)
		// Health pings: a connection silently dead after a network cut is replaced in seconds.
		cfg.HTTPClient = &http.Client{Transport: &http.Transport{Protocols: &p,
			HTTP2: &http.HTTP2Config{SendPingTimeout: 10 * time.Second, PingTimeout: 5 * time.Second}}}
	}
	w := &Worker{cfg: cfg, node: uuid.NewV7(), handlers: map[string]Handler{}, active: map[string]*running{}}
	for _, d := range cfg.Dispatchers {
		w.clients = append(w.clients, workerv1connect.NewWorkerServiceClient(cfg.HTTPClient, d, connect.WithGRPC()))
	}
	return w
}

// Handle registers a handler. The worker only claims types it has handlers for.
func (w *Worker) Handle(typ string, h Handler) { w.handlers[typ] = h }

// Run claims and runs jobs until ctx ends, then stops claiming and waits for running jobs
// to finish (graceful drain). Running jobs keep their own contexts and heartbeats meanwhile.
func (w *Worker) Run(ctx context.Context) error {
	if len(w.clients) == 0 || len(w.handlers) == 0 {
		return errors.New("worker needs at least one dispatcher and one handler")
	}
	types := make([]string, 0, len(w.handlers))
	for t := range w.handlers {
		types = append(types, t)
	}
	hbCtx, stopHeartbeats := context.WithCancel(context.Background())
	defer stopHeartbeats()
	go w.heartbeats(hbCtx)

	slots := make(chan struct{}, w.cfg.Concurrency)
	var jobs sync.WaitGroup
	for ctx.Err() == nil {
		select { // wait for at least one free slot
		case slots <- struct{}{}:
		case <-ctx.Done():
			continue
		}
		n := 1
		for n < w.cfg.Concurrency { // take any other free slots without waiting
			select {
			case slots <- struct{}{}:
				n++
				continue
			default:
			}
			break
		}
		claimed, err := w.claim(ctx, types, n)
		for range n - len(claimed) {
			<-slots
		}
		if err != nil && ctx.Err() == nil {
			w.cfg.Log.Warn("claim", "err", err)
			sleep(ctx, time.Second)
		}
		for _, j := range claimed {
			jobs.Go(func() {
				defer func() { <-slots }()
				w.run(j)
			})
		}
	}
	jobs.Wait()
	return nil
}

func (w *Worker) claim(ctx context.Context, types []string, n int) ([]*workerv1.Job, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second) // dispatch holds a claim open for up to 20 s
	defer cancel()
	req := connect.NewRequest(&workerv1.ClaimRequest{
		NodeId: w.node.String(), Cloud: w.cfg.Cloud, Queues: w.cfg.Queues, Types: types, MaxJobs: int32(n),
	})
	req.Header().Set("X-Worker-Name", w.cfg.Name)
	req.Header().Set("X-Worker-Version", w.cfg.Version)
	resp, err := w.client().Claim(ctx, req)
	if err != nil {
		w.failover()
		return nil, err
	}
	return resp.Msg.Jobs, nil
}

func (w *Worker) run(pj *workerv1.Job) {
	lease := &workerv1.Lease{JobId: pj.Id, LeaseToken: pj.LeaseToken}
	ctx, cancel := context.WithCancelCause(context.Background())
	ctx, stop := context.WithTimeoutCause(ctx, pj.TimeToDeadline.AsDuration(), errors.New("deadline passed"))
	defer stop()
	w.mu.Lock()
	w.active[pj.Id] = &running{lease: lease, cancel: cancel}
	w.mu.Unlock()

	err := call(ctx, w.handlers[pj.Type], Job{ID: pj.Id, Queue: pj.Queue, Type: pj.Type,
		Payload: pj.Payload, Attempt: int(pj.Attempt), EffectKey: pj.EffectKey})

	w.mu.Lock()
	delete(w.active, pj.Id)
	w.mu.Unlock()
	if errors.Is(context.Cause(ctx), errLeaseLost) {
		return // someone else may own the job now; any result would be fenced anyway
	}
	if err == nil {
		w.report(pj.Id, func(c workerv1connect.WorkerServiceClient, rctx context.Context) error {
			_, err := c.Complete(rctx, connect.NewRequest(&workerv1.CompleteRequest{Lease: lease}))
			return err
		})
		return
	}
	var p permanent
	w.report(pj.Id, func(c workerv1connect.WorkerServiceClient, rctx context.Context) error {
		_, err := c.Fail(rctx, connect.NewRequest(&workerv1.FailRequest{
			Lease: lease, Error: err.Error(), Permanent: errors.As(err, &p),
		}))
		return err
	})
}

// call runs a handler, turning a panic into an error so one bad job can't kill the worker.
func call(ctx context.Context, h Handler, j Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	if h == nil {
		return Permanent(fmt.Errorf("no handler for type %q", j.Type))
	}
	return h(ctx, j)
}

// report sends a result, retrying through every dispatcher for up to a minute. Complete and
// Fail are idempotent, so a retry after a lost response is safe.
func (w *Worker) report(jobID string, send func(workerv1connect.WorkerServiceClient, context.Context) error) {
	deadline := time.Now().Add(time.Minute)
	for wait := 100 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := send(w.client(), ctx)
		cancel()
		if err == nil {
			return
		}
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			w.cfg.Log.Info("result discarded: lease lost", "job", jobID)
			return
		}
		if time.Now().After(deadline) {
			w.cfg.Log.Error("result not reported; the lease will expire and the job will run again", "job", jobID, "err", err)
			return
		}
		w.failover()
		time.Sleep(wait)
	}
}

// heartbeats extends every held lease in one call per tick. If heartbeats fail for longer
// than a lease lasts, every lease is presumed lost and the handlers are stopped.
func (w *Worker) heartbeats(ctx context.Context) {
	lastOK := time.Now() // monotonic
	tick := time.NewTicker(w.cfg.HeartbeatEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		w.mu.Lock()
		held := make([]*running, 0, len(w.active))
		for _, r := range w.active {
			held = append(held, r)
		}
		w.mu.Unlock()
		if len(held) == 0 {
			lastOK = time.Now()
			continue
		}
		leases := make([]*workerv1.Lease, len(held))
		for i, r := range held {
			leases[i] = r.lease
		}
		rctx, cancel := context.WithTimeout(ctx, w.cfg.HeartbeatEvery)
		resp, err := w.client().Heartbeat(rctx, connect.NewRequest(&workerv1.HeartbeatRequest{NodeId: w.node.String(), Leases: leases}))
		cancel()
		if err != nil {
			w.failover()
			if time.Since(lastOK) > 3*w.cfg.HeartbeatEvery {
				for _, r := range held {
					r.cancel(errLeaseLost)
				}
			}
			continue
		}
		lastOK = time.Now()
		for i, st := range resp.Msg.Statuses {
			switch st.State {
			case workerv1.LeaseStatus_STATE_LOST:
				held[i].cancel(errLeaseLost)
			case workerv1.LeaseStatus_STATE_CANCEL_REQUESTED:
				held[i].cancel(errCancelled) // the handler stops; Fail then records "cancelled"
			}
		}
	}
}

func (w *Worker) client() workerv1connect.WorkerServiceClient {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clients[w.next%len(w.clients)]
}

func (w *Worker) failover() {
	w.mu.Lock()
	w.next++
	w.mu.Unlock()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
