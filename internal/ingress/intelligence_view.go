// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
	intel "github.com/opentacit/tacit/pkg/intelligence"
)

const (
	intelligencePublisherPageSize = 25
	intelligenceVersionPageSize   = 50
)

func (s *Server) pageIntelligence(r *http.Request, user oidc.Claims) view {
	snapshot := s.Intel.Snapshot()
	provider := strings.TrimSpace(r.URL.Query().Get("publisher"))
	state := r.URL.Query().Get("state")
	if state != "ready" && state != "waiting" {
		state = ""
	}

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">What crossed registry boundaries after a signed import: ` +
		`where public techniques travelled, and — only once five registries attest the exact same version — whether adoption helped. ` +
		`Importing registries are counted, never named.</p></div>`)
	b.WriteString(intelligenceTiles(snapshot))
	b.WriteString(intelligencePrivacyPanel(snapshot))

	if len(snapshot.Versions) == 0 {
		b.WriteString(ui.Panel("Technique intelligence", "",
			`<p class="empty">No import reports yet.</p>`+
				`<p class="hint">A registry starts reporting after an administrator turns on `+
				`<strong>Share aggregate outcomes for imported techniques</strong> under Settings → Access &amp; sharing. `+
				`The registry must also connect through Global Access.</p>`))
		return view{title: "Intelligence", nav: "intelligence", body: b.String(),
			crumbs: []ui.Crumb{{Label: "Intelligence"}}}
	}

	publisherPage := pageNumber(r.URL.Query().Get("publisher_page"))
	publisherPage, publishers := pagePublishers(snapshot.Publishers, publisherPage)
	b.WriteString(ui.Panel("Publishers",
		fmt.Sprintf("public provider identities · results earned in other registries · %d %s",
			len(snapshot.Publishers), plural(len(snapshot.Publishers), "publisher", "publishers")),
		intelligencePublisherTable(publishers)+intelligencePager(r.URL, "publisher_page", publisherPage,
			lastPage(len(snapshot.Publishers), intelligencePublisherPageSize), "Publisher pages")))

	versions := filterIntelligenceVersions(snapshot.Versions, provider, state)
	versionPage := pageNumber(r.URL.Query().Get("version_page"))
	versionPage, shown := pageVersions(versions, versionPage)
	b.WriteString(intelligenceFilters(snapshot.Publishers, provider, state))
	hint := fmt.Sprintf("%d of %d versions match", len(versions), len(snapshot.Versions))
	if len(versions) > 0 {
		first := (versionPage-1)*intelligenceVersionPageSize + 1
		hint = fmt.Sprintf("%d–%d of %d matching versions", first, first+len(shown)-1, len(versions))
	}
	b.WriteString(ui.Panel("Technique versions", hint,
		intelligenceVersionTable(shown)+intelligencePager(r.URL, "version_page", versionPage,
			lastPage(len(versions), intelligenceVersionPageSize), "Technique version pages")))

	return view{title: "Intelligence", nav: "intelligence", body: b.String(),
		crumbs: []ui.Crumb{{Label: "Intelligence"}}}
}

func intelligenceTiles(s IntelligenceSnapshot) string {
	ready, readyNote := "—", "no measured outcomes yet"
	adopted, adoptedNote := "—", "no measured outcomes yet"
	if s.ReadyVersions > 0 {
		ready, readyNote = ui.FmtCount(s.ReadyVersions), "of "+ui.FmtCount(len(s.Versions))+" imported versions"
		adopted = ui.FmtCount(s.Adopted)
		adoptedNote = fmt.Sprintf("%.0f%% helped · n=%s", helpedRate(s.Helped, s.Adopted), ui.FmtCount(s.SampleSize))
	}
	return ui.Tiles(
		ui.Tile("Outcome-ready versions", ready, readyNote),
		ui.Tile("Sharing registries", ui.FmtCount(s.SharingRegistries), "signed reporters"),
		ui.Tile("Importing registries", ui.FmtCount(s.ImportingRegistries), ui.FmtCount(s.ImportLinks)+" accepted links"),
		ui.Tile("Cross-org adoptions", adopted, adoptedNote),
	)
}

func intelligencePrivacyPanel(s IntelligenceSnapshot) string {
	latest := "no report received"
	if t, err := time.Parse(time.RFC3339Nano, s.LatestReceivedAt); err == nil {
		latest = ui.LocalTime(t, ui.LTStamp)
	}
	held := len(s.Versions) - s.ReadyVersions
	return ui.Panel("Release rules", "the same rules are enforced before values reach this page", kvTable([][2]string{
		{"Local report floor", fmt.Sprintf("%d adopted samples before one registry sends outcomes", intel.MinOutcomeSample)},
		{"Cross-org floor", fmt.Sprintf("%d reporting registries for the exact provider, entry and content hash", intel.CrossOrgMinRegistries)},
		{"Below the floor", fmt.Sprintf("import reach only; %d versions currently have all outcome values held back", held)},
		{"Latest signed snapshot", latest},
	}))
}

func intelligencePublisherTable(publishers []IntelligencePublisher) string {
	if len(publishers) == 0 {
		return `<p class="empty">No publishers match.</p>`
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="list data-table intelligence-publishers"><thead><tr>` +
		`<th>publisher</th><th class="num">imported by</th><th class="num">techniques</th>` +
		`<th class="num">versions ready</th><th class="num">shown → adopted → helped</th><th class="num">helped rate</th>` +
		`</tr></thead><tbody>`)
	for _, publisher := range publishers {
		funnel, rate := "—", `<span class="muted">no measured outcomes yet</span>`
		if publisher.ReadyVersions > 0 {
			funnel = fmt.Sprintf(`%s → %s → %s<br><span class="muted">%s dismissed</span>`,
				ui.FmtCount(publisher.Shown), ui.FmtCount(publisher.Adopted), ui.FmtCount(publisher.Helped),
				ui.FmtCount(publisher.Dismissed))
			rate = fmt.Sprintf(`%.0f%% <span class="muted">n=%s</span>`,
				helpedRate(publisher.Helped, publisher.Adopted), ui.FmtCount(publisher.SampleSize))
		}
		label := providerLabel(publisher.ProviderID)
		fmt.Fprintf(&b, `<tr data-search="%s"><td><a href="/intelligence?publisher=%s">%s</a>`+
			`<br><code class="muted intelligence-key">key %s</code><span class="row-meta">imported by %s registries</span></td>`+
			`<td class="num">%s</td><td class="num">%s</td>`+
			`<td class="num">%s of %s</td><td class="num">%s</td><td class="num">%s</td></tr>`,
			ui.Esc(strings.ToLower(publisher.ProviderID)), url.QueryEscape(publisher.ProviderID), ui.Esc(label),
			ui.Esc(publisher.KeyFingerprint), ui.FmtCount(publisher.ImportingRegistries),
			ui.FmtCount(publisher.ImportingRegistries), ui.FmtCount(publisher.Techniques),
			ui.FmtCount(publisher.ReadyVersions), ui.FmtCount(publisher.Versions), funnel, rate)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func intelligenceVersionTable(versions []IntelligenceVersion) string {
	if len(versions) == 0 {
		return `<p class="empty">No technique versions match these filters.</p>`
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="list data-table intelligence-versions"><thead><tr>` +
		`<th>technique</th><th>publisher</th><th>channel</th><th class="num">imported by</th>` +
		`<th>outcomes</th><th class="num">shown</th><th class="num">adopted</th><th class="num">helped</th>` +
		`<th class="num">dismissed</th><th class="num">helped rate</th>` +
		`</tr></thead><tbody>`)
	for _, version := range versions {
		outcomes := fmt.Sprintf(`<span class="muted">held until %d registries</span>`, intel.CrossOrgMinRegistries)
		shown, adopted, helped, dismissed, rate := "—", "—", "—", "—", "—"
		if version.Available {
			outcomes = fmt.Sprintf(`<span class="badge">ready</span> <span class="muted">%d registries</span>`, version.OutcomeRegistries)
			shown, adopted = ui.FmtCount(version.Shown), ui.FmtCount(version.Adopted)
			helped, dismissed = ui.FmtCount(version.Helped), ui.FmtCount(version.Dismissed)
			rate = fmt.Sprintf(`%.0f%% <span class="muted">n=%s</span>`,
				helpedRate(version.Helped, version.Adopted), ui.FmtCount(version.SampleSize))
		}
		fmt.Fprintf(&b, `<tr data-search="%s"><td><code>%s</code><br>`+
			`<span class="muted">version <code>%s</code></span><span class="row-meta">%s · imported by %s</span></td>`+
			`<td><a href="/intelligence?publisher=%s">%s</a><br><code class="muted">key %s</code></td>`+
			`<td><code>%s</code></td><td class="num">%s</td><td>%s</td>`+
			`<td class="num">%s</td><td class="num">%s</td><td class="num">%s</td>`+
			`<td class="num">%s</td><td class="num">%s</td></tr>`,
			ui.Esc(strings.ToLower(version.EntryID+" "+version.ProviderID+" "+version.ChannelID)),
			ui.Esc(entryLabel(version.EntryID)), ui.Esc(shortHash(version.ContentHash)), ui.Esc(providerLabel(version.ProviderID)),
			ui.FmtCount(version.ImportingRegistries),
			url.QueryEscape(version.ProviderID), ui.Esc(providerLabel(version.ProviderID)), ui.Esc(version.KeyFingerprint),
			ui.Esc(orDash(version.ChannelID)), ui.FmtCount(version.ImportingRegistries), outcomes,
			shown, adopted, helped, dismissed, rate)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func intelligenceFilters(publishers []IntelligencePublisher, provider, state string) string {
	ids := make([]string, 0, len(publishers))
	seen := map[string]bool{}
	for _, publisher := range publishers {
		if !seen[publisher.ProviderID] {
			seen[publisher.ProviderID] = true
			ids = append(ids, publisher.ProviderID)
		}
	}
	sort.Strings(ids)
	var options strings.Builder
	options.WriteString(`<option value="">All publishers</option>`)
	for _, id := range ids {
		selected := ""
		if id == provider {
			selected = " selected"
		}
		fmt.Fprintf(&options, `<option value="%s"%s>%s</option>`, ui.Esc(id), selected, ui.Esc(providerLabel(id)))
	}
	stateOptions := `<option value="">All states</option>` +
		option("ready", "Outcomes ready", state) + option("waiting", "Below privacy floor", state)
	reset := ""
	if provider != "" || state != "" {
		reset = ` <a class="btn" href="/intelligence">Reset</a>`
	}
	return `<form method="get" class="form-grid form-filter intelligence-filters">` +
		`<label>Publisher<select name="publisher">` + options.String() + `</select></label>` +
		`<label>Outcome state<select name="state">` + stateOptions + `</select></label>` +
		`<div class="full"><button class="btn" type="submit">Filter</button>` + reset + `</div></form>`
}

func filterIntelligenceVersions(versions []IntelligenceVersion, provider, state string) []IntelligenceVersion {
	out := make([]IntelligenceVersion, 0, len(versions))
	for _, version := range versions {
		if provider != "" && version.ProviderID != provider {
			continue
		}
		if state == "ready" && !version.Available || state == "waiting" && version.Available {
			continue
		}
		out = append(out, version)
	}
	return out
}

func pagePublishers(all []IntelligencePublisher, page int) (int, []IntelligencePublisher) {
	page = clampPage(page, lastPage(len(all), intelligencePublisherPageSize))
	start := (page - 1) * intelligencePublisherPageSize
	end := min(start+intelligencePublisherPageSize, len(all))
	return page, all[start:end]
}

func pageVersions(all []IntelligenceVersion, page int) (int, []IntelligenceVersion) {
	page = clampPage(page, lastPage(len(all), intelligenceVersionPageSize))
	start := (page - 1) * intelligenceVersionPageSize
	end := min(start+intelligenceVersionPageSize, len(all))
	return page, all[start:end]
}

func intelligencePager(u *url.URL, key string, page, last int, label string) string {
	if last <= 1 {
		return ""
	}
	href := func(target int) string {
		q := u.Query()
		q.Del(key)
		if target > 1 {
			q.Set(key, strconv.Itoa(target))
		}
		if encoded := q.Encode(); encoded != "" {
			return "/intelligence?" + encoded
		}
		return "/intelligence"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<nav class="btn-row table-pager" aria-label="%s">`, ui.Esc(label))
	if page > 1 {
		fmt.Fprintf(&b, `<a class="btn" href="%s" rel="prev">« Previous</a>`, ui.Esc(href(page-1)))
	}
	if page < last {
		fmt.Fprintf(&b, `<a class="btn" href="%s" rel="next">Next »</a>`, ui.Esc(href(page+1)))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

func pageNumber(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

func lastPage(total, size int) int {
	if total <= size {
		return 1
	}
	return (total + size - 1) / size
}

func clampPage(page, last int) int {
	if page < 1 {
		return 1
	}
	if page > last {
		return last
	}
	return page
}

func option(value, label, selectedValue string) string {
	selected := ""
	if value == selectedValue {
		selected = " selected"
	}
	return `<option value="` + ui.Esc(value) + `"` + selected + `>` + ui.Esc(label) + `</option>`
}

func providerLabel(id string) string {
	if parsed, err := url.Parse(id); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return id
}

func entryLabel(id string) string {
	if parsed, err := url.Parse(id); err == nil {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
	}
	return id
}

func shortHash(hash string) string {
	hash = strings.TrimPrefix(hash, "sha256:")
	if len(hash) > 10 {
		return hash[:10]
	}
	return hash
}

func helpedRate(helped, adopted int) float64 {
	if adopted == 0 {
		return 0
	}
	return 100 * float64(helped) / float64(adopted)
}
