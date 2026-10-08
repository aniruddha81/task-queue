// Package dispatch serves the worker API (proto/taskqueue/worker/v1). Workers never
// touch the database; every call here maps to one fenced statement in package queue.
package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/protobuf/types/known/durationpb"

	workerv1 "github.com/aniruddha81/task-queue/gen/taskqueue/worker/v1"
	"github.com/aniruddha81/task-queue/internal/queue"
)

const (
	leaseTTL   = 30 * time.Second
	stealAfter = 30 * time.Second // affinity jobs wait this long for their own cloud's workers
	maxWait    = 20 * time.Second // long-poll limit, under typical proxy idle timeouts
	pollEvery  = time.Second      // fallback when a NOTIFY is missed
)

var (
	claimed  = promauto.NewCounter(prometheus.CounterOpts{Name: "dispatch_jobs_claimed_total", Help: "Jobs handed to workers."})
	results  = promauto.NewCounterVec(prometheus.CounterOpts{Name: "dispatch_results_total", Help: "Complete and Fail calls by outcome."}, []string{"call", "outcome"})
	lostSeen = promauto.NewCounter(prometheus.CounterOpts{Name: "dispatch_heartbeat_leases_lost_total", Help: "Heartbeats for leases that were no longer held."})
	waited   = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "dispatch_job_wait_seconds",
		Help:    "Time from a job's run_at to its claim: submit-to-start latency for immediate jobs.",
		Buckets: prometheus.ExponentialBuckets(0.01, 2.5, 12)}, []string{"queue"})
)

// QueueDepth reports due, unclaimed jobs per queue at each scrape.
func QueueDepth(pool *pgxpool.Pool) prometheus.Collector { return depth{pool} }

type depth struct{ pool *pgxpool.Pool }

var depthDesc = prometheus.NewDesc("jobs_due_available", "Jobs due to run and not yet claimed.", []string{"queue"}, nil)

func (d depth) Describe(ch chan<- *prometheus.Desc) { ch <- depthDesc }

func (d depth) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := d.pool.Query(ctx, `SELECT queue, count(*) FROM jobs WHERE state = 'available' AND run_at <= now() GROUP BY queue`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		var n float64
		if rows.Scan(&q, &n) == nil {
			ch <- prometheus.MustNewConstMetric(depthDesc, prometheus.GaugeValue, n, q)
		}
	}
}

// Server implements workerv1connect.WorkerServiceHandler.
type Server struct {
	store *queue.Store
	log   *slog.Logger
	wake  waker
}

func New(store *queue.Store, log *slog.Logger) *Server {
	return &Server{store: store, log: log, wake: waker{ch: make(chan struct{})}}
}

func (s *Server) Claim(ctx context.Context, req *connect.Request[workerv1.ClaimRequest]) (*connect.Response[workerv1.ClaimResponse], error) {
	m := req.Msg
	node, err := uuid.Parse(m.NodeId)
	switch {
	case err != nil:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("node_id must be a UUID"))
	case m.Cloud != "aws" && m.Cloud != "azure":
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(`cloud must be "aws" or "azure"`))
	case len(m.Queues) == 0 || len(m.Types) == 0:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("queues and types are required"))
	case m.MaxJobs < 1 || m.MaxJobs > 100:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("max_jobs must be 1-100"))
	}
	version := req.Header().Get("X-Worker-Version")
	if err := s.store.SeenNode(ctx, queue.Node{ID: node, Name: req.Header().Get("X-Worker-Name"),
		Cloud: m.Cloud, Queues: m.Queues, Types: m.Types, Version: version}); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	p := queue.ClaimParams{Node: node, Cloud: m.Cloud, Queues: m.Queues, Types: m.Types,
		Max: int(m.MaxJobs), Lease: leaseTTL, StealAfter: stealAfter}
	for {
		woken := s.wake.wait() // before claiming, so a NOTIFY during the claim isn't missed
		jobs, err := s.store.Claim(ctx, p)
		if err == nil && len(jobs) > 0 { // claimed rows must reach the worker, even at the deadline
			claimed.Add(float64(len(jobs)))
			for _, j := range jobs {
				waited.WithLabelValues(j.Queue).Observe(max(j.WaitSeconds, 0))
			}
			return connect.NewResponse(&workerv1.ClaimResponse{Jobs: toProto(jobs)}), nil
		}
		if ctx.Err() != nil {
			return connect.NewResponse(&workerv1.ClaimResponse{}), nil // long poll ended empty
		}
		if err != nil {
			return nil, err
		}
		select {
		case <-woken:
		case <-time.After(pollEvery):
		case <-ctx.Done():
		}
	}
}

func toProto(jobs []queue.Claimed) []*workerv1.Job {
	out := make([]*workerv1.Job, len(jobs))
	for i, j := range jobs {
		out[i] = &workerv1.Job{
			Id: j.ID.String(), Queue: j.Queue, Type: j.Type, Payload: j.Payload,
			Attempt: int32(j.Attempt), LeaseToken: j.LeaseToken.String(), EffectKey: j.EffectKey(),
			LeaseTtl:       durationpb.New(seconds(j.LeaseSeconds)),
			TimeToDeadline: durationpb.New(seconds(j.DeadlineSeconds)),
		}
	}
	return out
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func (s *Server) Heartbeat(ctx context.Context, req *connect.Request[workerv1.HeartbeatRequest]) (*connect.Response[workerv1.HeartbeatResponse], error) {
	leases := make([]queue.Lease, len(req.Msg.Leases))
	for i, l := range req.Msg.Leases {
		var err error
		if leases[i], err = parseLease(l); err != nil {
			return nil, err
		}
	}
	states, err := s.store.Heartbeat(ctx, leases, leaseTTL)
	if err != nil {
		return nil, err
	}
	resp := &workerv1.HeartbeatResponse{Statuses: make([]*workerv1.LeaseStatus, len(states))}
	for i, st := range states {
		ps := workerv1.LeaseStatus_STATE_HELD
		switch st {
		case queue.Lost:
			ps = workerv1.LeaseStatus_STATE_LOST
			lostSeen.Inc()
		case queue.CancelRequested:
			ps = workerv1.LeaseStatus_STATE_CANCEL_REQUESTED
		}
		resp.Statuses[i] = &workerv1.LeaseStatus{State: ps}
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) Complete(ctx context.Context, req *connect.Request[workerv1.CompleteRequest]) (*connect.Response[workerv1.CompleteResponse], error) {
	l, err := parseLease(req.Msg.Lease)
	if err != nil {
		return nil, err
	}
	if err := result("complete", s.store.Complete(ctx, l)); err != nil {
		return nil, err
	}
	return connect.NewResponse(&workerv1.CompleteResponse{}), nil
}

func (s *Server) Fail(ctx context.Context, req *connect.Request[workerv1.FailRequest]) (*connect.Response[workerv1.FailResponse], error) {
	l, err := parseLease(req.Msg.Lease)
	if err != nil {
		return nil, err
	}
	if err := result("fail", s.store.Fail(ctx, l, req.Msg.Error, req.Msg.Permanent)); err != nil {
		return nil, err
	}
	return connect.NewResponse(&workerv1.FailResponse{}), nil
}

// result counts an outcome and maps a stale lease to FAILED_PRECONDITION.
func result(call string, err error) error {
	switch {
	case errors.Is(err, queue.ErrLeaseLost):
		results.WithLabelValues(call, "lease_lost").Inc()
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case err != nil:
		results.WithLabelValues(call, "error").Inc()
		return err
	}
	results.WithLabelValues(call, "ok").Inc()
	return nil
}

func parseLease(l *workerv1.Lease) (queue.Lease, error) {
	if l == nil {
		return queue.Lease{}, connect.NewError(connect.CodeInvalidArgument, errors.New("lease is required"))
	}
	id, err1 := uuid.Parse(l.JobId)
	tok, err2 := uuid.Parse(l.LeaseToken)
	if err1 != nil || err2 != nil {
		return queue.Lease{}, connect.NewError(connect.CodeInvalidArgument, errors.New("job_id and lease_token must be UUIDs"))
	}
	return queue.Lease{JobID: id, Token: tok}, nil
}

// Listen turns NOTIFY jobs_available into wake-ups for waiting Claim calls, reconnecting
// on errors until ctx ends. Claims also poll, so this only cuts latency.
func (s *Server) Listen(ctx context.Context, pool *pgxpool.Pool) {
	for ctx.Err() == nil {
		err := s.listen(ctx, pool)
		if ctx.Err() == nil {
			s.log.Warn("listen for job wake-ups", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func (s *Server) listen(ctx context.Context, pool *pgxpool.Pool) error {
	c, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	conn := c.Hijack() // a LISTENing connection must not go back to the pool
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN jobs_available"); err != nil {
		return err
	}
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		s.wake.wake()
	}
}

// waker lets any number of goroutines wait for the next wake().
type waker struct {
	mu sync.Mutex
	ch chan struct{}
}

func (w *waker) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

func (w *waker) wake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	close(w.ch)
	w.ch = make(chan struct{})
}
