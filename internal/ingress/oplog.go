// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Op is one served request, as the ingress records it for later analysis.
//
// What is here and what is not is the whole of the privacy argument for this
// file. The ingress terminates TLS, so it CAN see every byte of every request
// and response; what it retains is the envelope — who asked, for what path,
// with what result, how big and how long. No bodies, no query strings, no
// headers beyond the user agent. That is enough to answer the questions the
// operations log exists for (which clients connect, what breaks, which
// instances are healthy) and not enough to reconstruct what anyone was working
// on.
//
// The client address is truncated to a /24 (or /64) unless the operator turns
// that off: enough to tell one office from another, not enough to follow a
// person between them.
type Op struct {
	TS time.Time `json:"ts"`
	// Kind separates the two things worth recording: a request that was served,
	// and a tunnel coming up or going down. Tunnel churn is not a request, so it
	// used to be countable only in a separate file — which made that file the
	// only place some of the truth lived. Everything the console reports is now
	// a fold over this one log.
	Kind     string `json:"kind,omitempty"` // "" (a request), KindTunnelUp, KindTunnelDown
	Instance string `json:"instance"`
	Host     string `json:"host"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Bytes    int64  `json:"bytes"`
	MS       int64  `json:"ms"`
	UA       string `json:"ua,omitempty"`
	Client   string `json:"client,omitempty"` // truncated remote address
	Note     string `json:"note,omitempty"`   // why a request never reached a tunnel
}

// Op kinds. The empty string is a served request, so every line written before
// tunnel events existed still reads correctly.
const (
	KindRequest    = ""
	KindTunnelUp   = "tunnel-up"
	KindTunnelDown = "tunnel-down"
)

// IsRequest reports whether this record is a served request rather than a
// tunnel event.
func (o Op) IsRequest() bool { return o.Kind == KindRequest }

// OpLog is an append-only JSONL file with one rotated generation. It is the
// durable record; the metrics are a live summary that can be rebuilt from it.
type OpLog struct {
	// Logf, when set, reports that the log has stopped being written. Failures
	// here are otherwise silent on purpose — an ingress that stopped serving
	// traffic because it could not write its log would be trading a working
	// service for a complete record — but silence about the silence is what
	// turned a lost file handle into weeks of missing history. Set once at
	// construction and read under the lock.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	max      int64
	retain   time.Duration
	truncate bool // truncate client addresses

	// closed separates a log that was shut down from one whose file handle was
	// lost. Both leave f nil, and only one of them should be opened again.
	closed bool
	// warned keeps a broken log to one line in the operator's journal rather
	// than one per served request. A successful reopen clears it, so a log that
	// breaks twice is reported twice.
	warned bool
}

// OpenOpLog opens (or creates) the log under dir.
func OpenOpLog(dir string, maxBytes int64, retainDays int, fullClientIP bool) (*OpLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &OpLog{
		path:     filepath.Join(dir, "ops.jsonl"),
		max:      maxBytes,
		retain:   time.Duration(retainDays) * 24 * time.Hour,
		truncate: !fullClientIP,
	}
	if l.max <= 0 {
		l.max = 64 << 20
	}
	if err := l.reopenLocked(); err != nil {
		return nil, err
	}
	if _, err := l.Prune(); err != nil {
		return nil, err
	}
	return l, nil
}

// reopenLocked takes the live log for appending and measures what is already in
// it. Every path here ends in this call — startup, rotation, pruning, and an
// append that found no file — because a return that leaves f nil is a log that
// is dead until somebody restarts the ingress, and nothing says so.
//
// The size is read from the file rather than carried in from the caller, so the
// rotation ceiling is always measured against what is actually on disk.
func (l *OpLog) reopenLocked() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.f = nil
		return err
	}
	l.f, l.size, l.warned = f, 0, false
	if st, err := f.Stat(); err == nil {
		l.size = st.Size()
	}
	return nil
}

// warnLocked says once that the log is not being written.
func (l *OpLog) warnLocked(format string, args ...any) {
	if l.warned || l.Logf == nil {
		return
	}
	l.warned = true
	l.Logf(format, args...)
}

// Append records one operation. Failures are silent by design: an ingress that
// stopped serving traffic because it could not write its log would be trading a
// working service for a complete record, which is the wrong way round.
func (l *OpLog) Append(op Op) {
	if l == nil {
		return
	}
	if l.truncate {
		op.Client = truncateAddr(op.Client)
	}
	if len(op.UA) > 200 {
		op.UA = op.UA[:200]
	}
	b, err := json.Marshal(op)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.f == nil {
		// A rotate or a prune left this log without a file. Take one back now
		// rather than stay dead until a restart.
		if err := l.reopenLocked(); err != nil {
			l.warnLocked("the operations log is not being written: %v", err)
			return
		}
	}
	n, err := l.f.Write(b)
	if err != nil {
		l.warnLocked("the operations log is not being written: %v", err)
		return
	}
	l.size += int64(n)
	if l.size >= l.max {
		l.rotateLocked()
	}
}

func (l *OpLog) rotateLocked() {
	_ = l.f.Close()
	// A rename that fails leaves the live file where it was, so reopening gets
	// the same file back: the log grows past its ceiling until the next attempt,
	// which is better than not being written at all.
	_ = os.Rename(l.path, l.path+".1")
	if err := l.reopenLocked(); err != nil {
		l.warnLocked("the operations log could not be reopened after rotating it: %v", err)
	}
}

// Prune enforces the retention window: records older than it are dropped from
// the live log, and a rotated generation entirely past it is deleted.
//
// The size ceiling and the retention window are different promises and both are
// kept here. Rotation alone was not retention — it made a busy log keep two
// days while claiming ninety, and a quiet one keep everything forever. When the
// console says how long operations are held, this is what has to make it true.
func (l *OpLog) Prune() (dropped int, err error) {
	if l == nil || l.retain <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-l.retain)

	if st, statErr := os.Stat(l.path + ".1"); statErr == nil && st.ModTime().Before(cutoff) {
		_ = os.Remove(l.path + ".1")
	}

	// Rewrite the live log without its expired records. A whole-file rewrite is
	// affordable because it runs daily, not per request, and because the file
	// is bounded by the size ceiling in the first place.
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, nil
	}
	if err := l.f.Sync(); err != nil {
		return 0, err
	}
	src, err := os.Open(l.path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(l.path), "ops-*.jsonl")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // a no-op once renamed

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	w := bufio.NewWriter(tmp)
	for sc.Scan() {
		line := sc.Bytes()
		var op Op
		// A line that will not parse is kept: it is somebody's record and this
		// is not the place to decide it is worthless.
		if json.Unmarshal(line, &op) == nil && op.TS.Before(cutoff) {
			dropped++
			continue
		}
		_, _ = w.Write(append(append([]byte{}, line...), '\n'))
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if dropped == 0 {
		return 0, nil // nothing expired; leave the live file alone
	}
	if err := l.f.Close(); err != nil {
		return 0, err
	}
	// From here the live file has no handle, so every way out has to take one
	// again — including the failures. A return that left it closed made every
	// later Append fail without a word, and the log stayed dead until somebody
	// restarted the ingress.
	if err := os.Rename(tmpName, l.path); err != nil {
		_ = l.reopenLocked() // the live file is untouched; the pruning simply did not happen
		return 0, err
	}
	if err := os.Chmod(l.path, 0o600); err != nil {
		_ = l.reopenLocked()
		return 0, err
	}
	if err := l.reopenLocked(); err != nil {
		l.warnLocked("the operations log could not be reopened after pruning it: %v", err)
		return dropped, err
	}
	return dropped, nil
}

// OpFilter narrows a read of the log.
type OpFilter struct {
	Instance string
	Class    string // "2xx", "4xx", "5xx"; requests only
	Since    time.Time
	Limit    int
	// Offset skips this many of the newest matches before the page begins, so a
	// console can walk backwards through the log a page at a time instead of
	// rendering every match into one response.
	Offset int
	// Kind selects what to return: "request" for served requests (the default
	// view), "tunnel" for tunnel events, "" for both.
	Kind string
}

// Filter kinds, as the console asks for them.
const (
	FilterAny      = ""
	FilterRequests = "request"
	FilterTunnel   = "tunnel"
)

// maxScan bounds how much of the log a console page will read. The log is meant
// to be analysed with real tools; the console shows the recent tail.
const maxScan = 8 << 20

// Recent returns matching operations, newest first — the first page of a read,
// for callers that want a fixed tail and nothing else.
func (l *OpLog) Recent(f OpFilter) []Op {
	f.Offset = 0
	return l.Page(f).Ops
}

// OpPage is one page of a read, plus what a pager needs to say where it is.
type OpPage struct {
	// Ops is the page itself: the matches from Offset onward, newest first.
	Ops []Op
	// Total is how many records matched in the whole window read, not just on
	// this page — the number a pager divides into pages.
	Total int
	// Truncated reports that the log is longer than maxScan, so the window read
	// was its tail and Total counts matches within that tail. Without this a
	// page count would quietly present part of the log as all of it.
	Truncated bool
}

// Page reads the log once and returns the requested page of matches. The read
// is bounded twice over: by maxScan, which is how far back into the file it
// looks, and by Offset+Limit, which is how many records it holds in memory at a
// time. Neither the file nor the number of matches bounds the response.
func (l *OpLog) Page(f OpFilter) OpPage {
	if l == nil {
		return OpPage{}
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	// A ring of the last Offset+Limit matches: the log is read forwards (the
	// only way to parse it) and shown backwards, so the page being asked for is
	// only known once the end is reached. The ring overwrites in place — a
	// reslice-and-append would copy the whole window on every record past it.
	keep := f.Offset + f.Limit
	ring, oldest, total := make([]Op, 0, keep), 0, 0
	truncated := l.scanTail(f.Since, func(op Op) {
		if !f.matches(op) {
			return
		}
		total++
		if len(ring) < keep {
			ring = append(ring, op)
			return
		}
		ring[oldest] = op
		oldest = (oldest + 1) % keep
	})
	out := make([]Op, 0, len(ring))
	for i := len(ring) - 1; i >= 0; i-- {
		out = append(out, ring[(oldest+i)%len(ring)])
	}
	if f.Offset >= len(out) {
		return OpPage{Total: total, Truncated: truncated}
	}
	return OpPage{Ops: out[f.Offset:], Total: total, Truncated: truncated}
}

// scanTail reads the bounded tail of the log, calling fn for every record at or
// after since, oldest first. It reports whether the file was longer than the
// window read — which every caller that counts must pass on, or a count of the
// tail reads as a count of the log.
//
// One reader, three callers (Recent/Page, Scan, TimeSeries): the open, the seek
// past maxScan, the discarded partial line and the buffer size were three copies
// of the same fifteen lines, which is three places for a bound to drift.
func (l *OpLog) scanTail(since time.Time, fn func(Op)) (truncated bool) {
	if l == nil {
		return false
	}
	l.mu.Lock()
	if l.f != nil {
		_ = l.f.Sync()
	}
	l.mu.Unlock()

	fh, err := os.Open(l.path)
	if err != nil {
		return false
	}
	defer func() { _ = fh.Close() }()
	st, err := fh.Stat()
	if err != nil {
		return false
	}
	truncated = st.Size() > maxScan
	if truncated {
		if _, err := fh.Seek(st.Size()-maxScan, 0); err != nil {
			return truncated
		}
	}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if truncated {
		sc.Scan() // discard the partial line the seek landed in
	}
	for sc.Scan() {
		var op Op
		if err := json.Unmarshal(sc.Bytes(), &op); err != nil {
			continue
		}
		if !since.IsZero() && op.TS.Before(since) {
			continue
		}
		fn(op)
	}
	return truncated
}

func (f OpFilter) matches(op Op) bool {
	if f.Instance != "" && op.Instance != f.Instance {
		return false
	}
	switch f.Kind {
	case FilterRequests:
		if !op.IsRequest() {
			return false
		}
	case FilterTunnel:
		if op.IsRequest() {
			return false
		}
	}
	if f.Class != "" && (!op.IsRequest() || classOf(op.Status) != f.Class) {
		return false
	}
	if !f.Since.IsZero() && op.TS.Before(f.Since) {
		return false
	}
	return true
}

// Scan calls fn for every record at or after since, oldest first.
//
// It is what rebuilds the live counters at startup, and the reason there is no
// second file holding them: the summary the console shows is a fold over this
// log, so it can always be recomputed and can never quietly disagree with it.
// The read is bounded by the same maxScan ceiling as everything else, so a very
// large log costs a fixed amount of startup rather than an unbounded one.
func (l *OpLog) Scan(since time.Time, fn func(Op)) {
	_ = l.scanTail(since, fn)
}

// Close flushes and closes the log.
func (l *OpLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// truncateAddr keeps the network and drops the host: 203.0.113.47 becomes
// 203.0.113.0, and an IPv6 address keeps its /64.
func truncateAddr(addr string) string {
	if addr == "" {
		return ""
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return net.IP(append(append([]byte{}, v4[:3]...), 0)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// shortUA reduces a user agent to something a table column can hold, keeping
// the part that identifies the client rather than the platform noise.
func shortUA(ua string) string {
	if ua == "" {
		return ""
	}
	if i := strings.IndexAny(ua, " ("); i > 0 {
		ua = ua[:i]
	}
	if len(ua) > 40 {
		ua = ua[:40]
	}
	return ua
}

// --- request rate over time -------------------------------------------------

// RateBucket is one x position on the overview's chart: when the bucket starts,
// and how many requests fell in it per series.
type RateBucket struct {
	Start time.Time
	// Counts is by series key — the instance name, or "" for the aggregate.
	Counts map[string]int
}

// RateSeries is a bucketed count of served requests over a window.
type RateSeries struct {
	From, To  time.Time
	Bucket    time.Duration
	Buckets   []RateBucket
	Names     []string // series present, busiest first
	Total     int
	Truncated bool
}

// Rate buckets the log's requests into a window, either as one aggregate series
// or split per instance.
//
// The read is bounded by maxScan like every other, and the number of buckets is
// bounded by the caller's choice of bucket width — so a chart of an hour and a
// chart of a month cost the same to render and to send. Only served requests
// count: tunnel connects and drops are a different question, asked on the
// Operations page with kind=tunnel.
func (l *OpLog) Rate(from, to time.Time, bucket time.Duration, byInstance bool) RateSeries {
	res := RateSeries{From: from, To: to, Bucket: bucket}
	if l == nil || bucket <= 0 || !to.After(from) {
		return res
	}
	n := int(to.Sub(from) / bucket)
	if rem := to.Sub(from) % bucket; rem > 0 {
		n++
	}
	if n <= 0 {
		return res
	}
	res.Buckets = make([]RateBucket, n)
	for i := range res.Buckets {
		res.Buckets[i] = RateBucket{Start: from.Add(time.Duration(i) * bucket), Counts: map[string]int{}}
	}
	totals := map[string]int{}
	res.Truncated = l.scanTail(from, func(op Op) {
		if !op.IsRequest() || op.TS.Before(from) || !op.TS.Before(to) {
			return
		}
		i := int(op.TS.Sub(from) / bucket)
		if i < 0 || i >= n {
			return
		}
		key := ""
		if byInstance {
			key = op.Instance
		}
		res.Buckets[i].Counts[key]++
		totals[key]++
		res.Total++
	})
	for name := range totals {
		res.Names = append(res.Names, name)
	}
	// Busiest first, ties by name so the order is stable between reads.
	sort.Slice(res.Names, func(i, j int) bool {
		a, b := res.Names[i], res.Names[j]
		if totals[a] != totals[b] {
			return totals[a] > totals[b]
		}
		return a < b
	})
	return res
}
