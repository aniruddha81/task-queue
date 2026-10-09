package scheduler

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/pgtest"
)

// blackhole forwards TCP to an upstream until it is cut; then it silently swallows bytes in
// both directions, and closes too, like a network partition: the other side's FIN never
// arrives, so a client's socket stays open and its reads hang.
type blackhole struct {
	cut atomic.Bool
	l   net.Listener
}

func newBlackhole(t *testing.T, upstream string) *blackhole {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &blackhole{l: l}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", upstream)
			if err != nil {
				c.Close()
				continue
			}
			go b.pipe(up, c)
			go b.pipe(c, up)
		}
	}()
	return b
}

func (b *blackhole) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if err != nil {
			if !b.cut.Load() {
				dst.Close()
			}
			return
		}
		if !b.cut.Load() {
			dst.Write(buf[:n])
		}
	}
}

// A network cut leaves the leader's database connections silently dead. Every step of
// leading must still end in bounded time, so that once the network heals the scheduler
// leads again. (Found by a chaos run in which both schedulers, cut off in turn, hung on
// dead connections and nobody reaped expired leases for 7 minutes.)
func TestLeadsAgainAfterSilentlyDeadConnections(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	cfg := pool.Config().Copy()
	var holes []*blackhole
	proxy := func(host *string, port *uint16) {
		b := newBlackhole(t, net.JoinHostPort(*host, strconv.Itoa(int(*port))))
		holes = append(holes, b)
		*host, *port = "127.0.0.1", uint16(b.l.Addr().(*net.TCPAddr).Port)
	}
	proxy(&cfg.ConnConfig.Host, &cfg.ConnConfig.Port)
	for _, f := range cfg.ConnConfig.Fallbacks { // every node of a cluster, to find the primary
		proxy(&f.Host, &f.Port)
	}
	proxied, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer proxied.Close()

	s := New(proxied, quiet, Config{TTL: 3 * time.Second, Every: 100 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, "leading", 10*time.Second, func() bool { h, _ := leader(t, pool); return h == s.me })
	_, epoch := leader(t, pool)

	for _, b := range holes {
		b.cut.Store(true)
	}
	// Longer than the server's idle_in_transaction_session_timeout (10 s): in a real partition
	// the server's "terminating connection" message is lost, not delayed.
	time.Sleep(12 * time.Second)
	for _, b := range holes {
		b.cut.Store(false) // the old connections stay broken: their bytes are gone
	}
	waitFor(t, "leading again after the network healed", 20*time.Second, func() bool {
		h, e := leader(t, pool)
		return h == s.me && e > epoch
	})
}
