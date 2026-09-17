// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/ui"
)

// The small render helpers the console pages share. Nothing here decides
// anything; it exists so the page functions read as a sequence of panels rather
// than as string building.

func (s *Server) instanceTable(instances []Instance, online map[string]bool, limit int) string {
	if len(instances) == 0 {
		// The empty state's job is to say what to do next, and the only thing
		// an operator of THIS console can do is hand someone the tunnel
		// address — enrolment happens entirely at the registry's end.
		return ui.Panel("Instances", "",
			`<p class="empty">Nothing published yet.</p>`+
				`<p class="hint">A registry enrols itself. On the machine running one, turn on `+
				`<strong>Access through the `+ui.Esc(product.Name())+` proxy</strong> in Settings and point it at `+
				`<code>`+ui.Esc(s.tunnelHost())+`</code>. It is given a hostname on connecting — `+
				`there is no name to approve here and no token to issue.</p>`)
	}
	shown := instances
	hint := ""
	if limit > 0 && len(instances) > limit {
		shown = instances[:limit]
		hint = fmt.Sprintf(`showing %d of %d · <a href="/instances">all instances</a>`, limit, len(instances))
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="list data-table"><thead><tr>` +
		`<th>name</th><th>state</th><th>build</th><th>source</th><th class="num">members</th>` +
		`<th class="num">requests</th>` +
		`<th class="num">p95</th><th class="num">errors</th><th>last seen</th></tr></thead><tbody>`)
	for _, in := range shown {
		m := s.Metrics.For(in.Name)
		st := s.StatusOf(in.Name)
		// Where this registry connects from. The live tunnel is the current
		// answer; the stored one keeps the column meaningful for an instance
		// that is offline, which is when an operator is most likely reading it.
		source := sourceHost(firstNonEmpty(st.RemoteStr, in.Source))
		fmt.Fprintf(&b, `<tr data-href="/instances/%s" data-search="%s"><td>%s</td><td>%s</td><td><code>%s</code></td>`+
			`<td><code>%s</code></td><td class="num">%s</td>`+
			`<td class="num">%s</td><td class="num">%d ms</td><td class="num">%.1f%%</td><td>%s</td></tr>`,
			url.PathEscape(in.Name), ui.Esc(strings.ToLower(in.Name+" "+in.Owner+" "+source)), ui.Esc(in.Name),
			stateBadge(in, online[in.Name]), ui.Esc(orDash(firstNonEmpty(st.Version, in.Version))),
			ui.Esc(orDash(source)), memberCell(in),
			ui.FmtCount(int(m.Requests)), m.P95(), 100*m.ErrorRate(), stamp(lastActivity(in, m)))
	}
	b.WriteString(`</tbody></table></div>`)
	return ui.Panel("Instances", hint, b.String())
}

// memberCell is an instance's reported size in the table: machines seen in its
// last MONTH, because that is the "how big is this tenant" number. The other
// windows are on the instance page, where there is room to say which is which.
//
// A registry that has never reported gets a dash rather than a zero — one is
// "not said", the other is a claim about the tenant, and an old client that
// does not know the message would otherwise read as an empty organization. A
// figure that has gone stale keeps its number and says when, since a month-old
// count from a registry that is still offline is history, not news.
func memberCell(in Instance) string {
	if in.MembersAt.IsZero() {
		return "—"
	}
	// A registry from before the month figure existed reports only the week; its
	// own number is better than a dash that hides a tenant it does know about.
	n := in.MembersMonth
	if !in.MembersWindows {
		n = in.Members
	}
	cell := ui.FmtCount(n)
	if age := time.Since(in.MembersAt); age > staleMembers {
		cell += ` <span class="muted" title="reported ` + ui.Esc(stamp(in.MembersAt)) + `">·&nbsp;stale</span>`
	}
	return cell
}

// staleMembers is when a reported figure stops being current. A connected
// registry repeats itself every ten minutes, so an hour of silence means it has
// been away rather than quiet.
const staleMembers = time.Hour

// sourceHost drops the port from a connection's address. A directly-dialled
// tunnel carries an ephemeral port that changes on every reconnection and
// identifies nothing; the address is the part that says where a registry lives.
// A forwarded address arrives without one already.
func sourceHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func stateBadge(in Instance, online bool) string {
	switch {
	case in.Disabled:
		return `<span class="status serious">suspended</span>`
	case online:
		return `<span class="badge">online</span>`
	default:
		return `<span class="muted">offline</span>`
	}
}

func statusSentence(in Instance, st Status) string {
	switch {
	case in.Disabled:
		return "Suspended. The tunnel is refused and the address answers 403 until it is resumed."
	case st.Online:
		return "Connected since " + ui.LocalTime(st.Since, ui.LTStamp) +
			", holding " + strconv.Itoa(st.Parked) + " spare connections."
	default:
		return "Not connected. The address answers 502 until the registry dials back in."
	}
}

func opsTable(ops []Op, withInstance bool) string {
	if len(ops) == 0 {
		return `<p class="empty">Nothing served yet.</p>`
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="list data-table"><thead><tr><th>when</th>`)
	if withInstance {
		b.WriteString(`<th>instance</th>`)
	}
	b.WriteString(`<th>method</th><th>path</th><th class="num">status</th><th class="num">ms</th>` +
		`<th class="num">bytes</th><th>client</th></tr></thead><tbody>`)
	for _, op := range ops {
		b.WriteString(`<tr data-search="` + ui.Esc(strings.ToLower(op.Instance+" "+op.Path+" "+op.UA)) + `">`)
		fmt.Fprintf(&b, `<td><code>%s</code></td>`, ui.LocalTime(op.TS, ui.LTHMS))
		if withInstance {
			fmt.Fprintf(&b, `<td><a href="/instances/%s">%s</a></td>`, url.PathEscape(op.Instance), ui.Esc(op.Instance))
		}
		note := ""
		if op.Note != "" {
			note = ` <span class="muted">(` + ui.Esc(op.Note) + `)</span>`
		}
		fmt.Fprintf(&b, `<td><code>%s</code></td><td><code>%s</code></td><td class="num">%s</td>`+
			`<td class="num">%d</td><td class="num">%s</td><td><code>%s</code></td></tr>`,
			ui.Esc(op.Method), ui.Esc(truncate(op.Path, 60)), statusCell(op.Status)+note,
			op.MS, formatBytes(op.Bytes), ui.Esc(orDash(shortUA(op.UA))))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func statusCell(status int) string {
	cls := ""
	if status >= 500 {
		cls = ` class="status serious"`
	}
	return `<span` + cls + `>` + strconv.Itoa(status) + `</span>`
}

func classTable(m InstanceMetrics) string {
	if m.Requests == 0 {
		return `<p class="empty">No requests yet.</p>`
	}
	var b strings.Builder
	b.WriteString(`<table class="list"><tbody>`)
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		n := m.ByClass[class]
		fmt.Fprintf(&b, `<tr><td class="muted">%s</td><td class="num">%s</td><td class="num">%.1f%%</td></tr>`,
			class, ui.FmtCount(int(n)), 100*float64(n)/float64(m.Requests))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// dayBars draws the rolling day as an inline SVG: one bar per hour, the 5xx
// share stacked on top in the bad colour. No chart library, same as the
// dashboard's own sparklines.
func dayBars(day []HourBucket) string {
	var peak int64 = 1
	var total int64
	for _, h := range day {
		total += h.Requests
		if h.Requests > peak {
			peak = h.Requests
		}
	}
	if total == 0 {
		return `<p class="empty">No traffic in the last 24 hours.</p>`
	}
	const w, h, gap = 24.0, 60.0, 3.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="viz" viewBox="0 0 %g %g" preserveAspectRatio="none" role="img" aria-label="requests per hour">`,
		float64(len(day))*w, h+14)
	for i, slot := range day {
		x := float64(i) * w
		barH := h * float64(slot.Requests) / float64(peak)
		if slot.Requests > 0 && barH < 2 {
			barH = 2
		}
		errH := 0.0
		if slot.Errors > 0 {
			errH = h * float64(slot.Errors) / float64(peak)
			if errH < 2 {
				errH = 2
			}
		}
		fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" fill="var(--axis)"><title>%d requests</title></rect>`,
			x, h-barH, w-gap, barH, slot.Requests)
		if errH > 0 {
			fmt.Fprintf(&b, `<rect x="%g" y="%g" width="%g" height="%g" fill="var(--bad)"><title>%d server errors</title></rect>`,
				x, h-errH, w-gap, errH, slot.Errors)
		}
	}
	fmt.Fprintf(&b, `<text x="0" y="%g" fill="var(--muted)" font-size="9">24h ago</text>`, h+11)
	fmt.Fprintf(&b, `<text x="%g" y="%g" fill="var(--muted)" font-size="9" text-anchor="end">now</text>`,
		float64(len(day))*w-gap, h+11)
	b.WriteString(`</svg>`)
	return b.String()
}

func classOptions(selected string) string {
	var b strings.Builder
	for _, opt := range []struct{ value, label string }{
		{"", "any"}, {"2xx", "2xx served"}, {"3xx", "3xx redirect"},
		{"4xx", "4xx refused"}, {"5xx", "5xx failed"},
	} {
		sel := ""
		if opt.value == selected {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, opt.value, sel, opt.label)
	}
	return b.String()
}

func lastActivity(in Instance, m InstanceMetrics) time.Time {
	if m.LastRequest.After(in.LastSeen) {
		return m.LastRequest
	}
	return in.LastSeen
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return `<span class="muted">never</span>`
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Local().Format("2 Jan 15:04")
	}
}

func offlineNote(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n) + " offline"
}

// kvTable is the console's two-column table: a muted label on the left, its
// value on the right. Values are markup — a caller holding a link or a code
// span passes it straight through, a caller holding text escapes it first, and
// kvCodeTable does both for the common case.
func kvTable(rows [][2]string) string {
	var b strings.Builder
	b.WriteString(`<table class="list"><tbody>`)
	for _, row := range rows {
		fmt.Fprintf(&b, `<tr><td class="muted">%s</td><td>%s</td></tr>`, row[0], row[1])
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// kvCodeTable is kvTable for values that are plain text meant to read as
// literals: a hostname, an address, a setting as it was written down.
func kvCodeTable(rows [][2]string) string {
	out := make([][2]string, len(rows))
	for i, row := range rows {
		out[i] = [2]string{row[0], `<code>` + ui.Esc(row[1]) + `</code>`}
	}
	return kvTable(out)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// tunnelTable renders tunnel events. They share the operations log with served
// requests but not its shape — there is no path, no status and no size, and the
// duration is how long the tunnel lasted rather than how long a request took.
func tunnelTable(ops []Op) string {
	if len(ops) == 0 {
		return `<p class="empty">No tunnel events recorded.</p>`
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="list data-table"><thead><tr>` +
		`<th>when</th><th>instance</th><th>event</th><th>held for</th><th>from</th></tr></thead><tbody>`)
	for _, op := range ops {
		event := `<span class="badge">connected</span>`
		held := ""
		if op.Kind == KindTunnelDown {
			event = `<span class="muted">dropped</span>`
			held = duration(op.MS)
		} else if op.Note != "" {
			event += ` <code>` + ui.Esc(op.Note) + `</code>` // the build that connected
		}
		fmt.Fprintf(&b, `<tr data-search="%s"><td><code>%s</code></td>`+
			`<td><a href="/instances/%s">%s</a></td><td>%s</td><td>%s</td><td><code>%s</code></td></tr>`,
			ui.Esc(strings.ToLower(op.Instance)), op.TS.Local().Format("2 Jan 15:04:05"),
			url.PathEscape(op.Instance), ui.Esc(op.Instance), event, held, ui.Esc(orDash(op.Client)))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// duration renders a tunnel's lifetime at a resolution a person cares about.
func duration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func kindOptions(selected string) string {
	var b strings.Builder
	for _, opt := range []struct{ value, label string }{
		{FilterRequests, "requests"}, {FilterTunnel, "tunnel events"}, {FilterAny, "everything"},
	} {
		sel := ""
		if opt.value == selected {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, opt.value, sel, opt.label)
	}
	return b.String()
}

// coverageNote is the small print under a counter: what window it covers. The
// figures are a fold over the operations log rather than lifetime totals, and
// a tile that implied otherwise would be claiming more than it knows.
func coverageNote(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := time.Since(since)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("last %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("last %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("last %dd", int(d.Hours()/24))
	}
}

func coverageSince(since time.Time) string {
	if since.IsZero() {
		return "nothing recorded yet"
	}
	return "since " + since.Local().Format("2 Jan 15:04")
}
