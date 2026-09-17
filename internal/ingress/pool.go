// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// parked is one idle connection and when it was parked, so the pool can retire
// it before something in the path does.
type parked struct {
	conn net.Conn
	at   time.Time
}

// connPool holds one instance's parked data connections.
//
// The only subtle part is liveness. A connection can sit here for hours, and
// the registry at the other end may have been restarted, upgraded or moved in
// the meantime. Handing a dead socket to http.Transport turns into a 502 for a
// request that would otherwise have worked, so every connection is checked on
// the way out: a parked connection should have nothing readable on it, so a
// read with a deadline already past distinguishes "idle and healthy" (timeout)
// from "closed" (EOF or error) — and from "the peer sent something unasked",
// which is a protocol violation and equally a reason to drop it.
type connPool struct {
	mu      sync.Mutex
	free    []parked
	waiters []chan net.Conn
	closed  bool

	// maxIdle is how long a connection may sit unused. It exists because a
	// tunnel usually runs through someone else's web server, and those close
	// idle proxied connections on their own schedule — Apache's ProxyTimeout
	// defaults to 120 seconds. A connection killed that way is not always
	// closed cleanly from our side, so the liveness probe cannot see it: the
	// request goes out, nothing comes back, and the cost is a timeout rather
	// than a reconnect. Retiring connections first, on our own clock, keeps
	// that from ever being the request's problem.
	maxIdle time.Duration

	// want is called when the pool is running low, with how many more
	// connections would restore the target. It reaches the registry over the
	// control connection.
	want func(n int)
	// target is how many parked connections the pool tries to keep.
	target int
}

var errPoolClosed = errors.New("tunnel closed")

func newConnPool(target int, maxIdle time.Duration, want func(n int)) *connPool {
	if target <= 0 {
		target = 8
	}
	if maxIdle <= 0 {
		maxIdle = 45 * time.Second
	}
	return &connPool{target: target, maxIdle: maxIdle, want: want}
}

// Put parks a connection, or hands it straight to a waiting request.
func (p *connPool) Put(c net.Conn) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	if len(p.waiters) > 0 {
		w := p.waiters[0]
		p.waiters = p.waiters[1:]
		p.mu.Unlock()
		w <- c
		return
	}
	p.free = append(p.free, parked{conn: c, at: time.Now()})
	p.mu.Unlock()
}

// Retire closes connections that have been idle too long and asks for
// replacements, so the pool is always warm rather than full of sockets that
// something upstream has quietly abandoned.
func (p *connPool) Retire() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	cutoff := time.Now().Add(-p.maxIdle)
	keep := p.free[:0]
	var stale []net.Conn
	for _, pc := range p.free {
		if pc.at.Before(cutoff) {
			stale = append(stale, pc.conn)
			continue
		}
		keep = append(keep, pc)
	}
	p.free = keep
	short := p.target - len(p.free)
	p.mu.Unlock()

	for _, c := range stale {
		_ = c.Close()
	}
	if short > 0 {
		p.topUp(short)
	}
}

// Get returns a live parked connection, waiting for one if the pool is empty.
// It is the DialContext of the instance's http.Transport: "dialling" here means
// taking a connection the registry already opened towards us.
func (p *connPool) Get(ctx context.Context) (net.Conn, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errPoolClosed
		}
		if n := len(p.free); n > 0 {
			pc := p.free[n-1]
			p.free = p.free[:n-1]
			short := len(p.free)
			p.mu.Unlock()
			c := pc.conn
			if time.Since(pc.at) > p.maxIdle || !alive(c) {
				_ = c.Close()
				p.topUp(1)
				continue
			}
			// Taking one leaves the pool shallower; ask for a replacement now
			// rather than when it runs dry.
			if short < p.target {
				p.topUp(1)
			}
			return c, nil
		}
		ch := make(chan net.Conn, 1)
		p.waiters = append(p.waiters, ch)
		p.mu.Unlock()

		p.topUp(p.target)
		select {
		case c, ok := <-ch:
			if !ok {
				return nil, errPoolClosed // Close() closed the waiter
			}
			if !alive(c) {
				_ = c.Close()
				continue
			}
			return c, nil
		case <-ctx.Done():
			p.drop(ch)
			return nil, ctx.Err()
		}
	}
}

// drop removes a waiter that gave up, returning any connection that raced in.
func (p *connPool) drop(ch chan net.Conn) {
	p.mu.Lock()
	for i, w := range p.waiters {
		if w == ch {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			p.mu.Unlock()
			return
		}
	}
	p.mu.Unlock()
	// Already handed a connection: park it again rather than leak it.
	select {
	case c, ok := <-ch:
		if ok && c != nil {
			p.Put(c)
		}
	default:
	}
}

func (p *connPool) topUp(n int) {
	if n <= 0 || p.want == nil {
		return
	}
	p.want(n)
}

// Len is the number of parked connections, for the console.
func (p *connPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.free)
}

// Close drops every parked connection and fails every waiter.
func (p *connPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	free, waiters := p.free, p.waiters
	p.free, p.waiters = nil, nil
	p.mu.Unlock()

	for _, pc := range free {
		_ = pc.conn.Close()
	}
	for _, w := range waiters {
		close(w)
	}
}

// alive reports whether a parked connection is still usable. See the type
// comment for why a read that times out is the healthy answer.
func alive(c net.Conn) bool {
	if err := c.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
		return false
	}
	defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	var b [1]byte
	n, err := c.Read(b[:])
	if n > 0 {
		return false // unsolicited data on a parked connection
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true // nothing to read, which is exactly right
	}
	if errors.Is(err, io.EOF) {
		return false
	}
	return err == nil
}
