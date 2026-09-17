// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import "net/http"

// home is what "/" renders.
//
// The outcomes dashboard for an organization. For a signed-in owner of a
// single-member registry, the user guide instead: they set the place up minutes
// ago and have nothing to see in a funnel yet, and the guide is the one page
// that answers what to do next. Every other destination is one click away in
// the same bar.
// home is Outcomes, for everybody.
//
// A single-member registry used to land its owner on the user guide's table of
// contents — a reasonable answer when Outcomes met them with fourteen empty
// panels, and the wrong one now that it opens with what the registry has and
// what to do next. Twenty-six chapters is a manual; the reader wanted a next
// step, and the cold-start panel is one, with the guide a click away in the nav
// beside it.
func (s *Server) home() http.HandlerFunc {
	return s.htmlView(s.pageOutcomes)
}

// membersOrTeam serves the organization's member list, or redirects to the page
// that carries it on a smaller registry.
func (s *Server) membersOrTeam() http.HandlerFunc {
	members := s.htmlView(s.pageMembers)
	return func(w http.ResponseWriter, r *http.Request) {
		if home := s.membersHome(); home != "/members" {
			http.Redirect(w, r, s.cfg().BasePath+home, http.StatusFound)
			return
		}
		members(w, r)
	}
}

func (s *Server) mountPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.home())
	mux.HandleFunc("GET /outcomes", s.htmlView(s.pageOutcomes))
	mux.HandleFunc("GET /learning", s.htmlView(s.pageLearning))
	mux.HandleFunc("GET /learning/workflows", s.htmlView(s.pageWorkflowTraces))
	mux.HandleFunc("GET /usage", s.htmlView(s.pageUsage))
	// The sibling views, as paths like every other section's — /outcomes/cohorts,
	// /techniques/map. One handler serves them all: the decrypt the sealed ledger
	// needs happens once per page load either way, so the path costs nothing the
	// query parameter saved.
	mux.HandleFunc("GET /usage/work", s.htmlView(s.pageUsage))
	mux.HandleFunc("GET /usage/cost", s.htmlView(s.pageUsage))
	mux.HandleFunc("GET /usage/results", s.htmlView(s.pageUsage))
	mux.HandleFunc("GET /usage/models", s.htmlView(s.pageUsage))
	mux.HandleFunc("GET /usage/tools", s.htmlView(s.pageUsage))
	mux.HandleFunc("GET /usage/allowance", s.htmlView(s.pageUsage))
	// Trends stopped being a place. It was an axis pretending to be a
	// destination: every view here now carries the series it already drew, and
	// a page whose only content is "the same numbers again, over time" is one
	// the member has to leave to ask anything else.
	//
	// It moves with its query intact, and it moves with a 301 rather than a
	// 302, because the old address is not coming back and a bookmark should
	// learn that once.
	mux.HandleFunc("GET /usage/trends", func(w http.ResponseWriter, r *http.Request) {
		to := s.cfg().BasePath + "/usage"
		if q := r.URL.RawQuery; q != "" {
			to += "?" + q
		}
		http.Redirect(w, r, to, http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /usage/data", s.handleUsageData)
	mux.HandleFunc("GET /usage/sessions", s.handleSessionsData)
	// Which sealed ledger belongs to the signed-in reader, and the one-time
	// claim that says so (ledger.go). Neither carries the key that opens it.
	mux.HandleFunc("GET /usage/ledger/mine", s.handleLedgerMine)
	mux.HandleFunc("POST /usage/ledger/bind", s.handleLedgerBind)
	mux.HandleFunc("GET /outcomes/events", s.htmlView(s.pageEvents))
	mux.HandleFunc("GET /outcomes/events/data", s.handleEventsData)
	mux.HandleFunc("POST /v1/invite", s.keyAuthed(s.handleInvite))

	mux.HandleFunc("GET /team", s.htmlView(s.pageTeam))
	// One subject, one destination. A registry that is one member's — or one
	// they have opened to their team — keeps everything about who is here on
	// /team, so /members sends its readers there rather than being a second
	// door to the same sections. Old links and bookmarks keep working.
	mux.HandleFunc("GET /members", s.membersOrTeam())
	mux.HandleFunc("POST /members/mint", s.adminOnly(s.handleMemberMint))
	mux.HandleFunc("POST /members/{action}/{id}", s.adminOnly(s.handleMemberAction))
	// One link, two clients (M4): the installer for `curl`, the invitation page
	// for a browser. The POST is where the token is actually spent.
	mux.HandleFunc("GET /join/{token}", s.handleJoin)
	mux.HandleFunc("POST /join/{token}", s.handleJoinAccept)
	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("POST /v1/join/exchange", s.handleJoinExchange)
	mux.HandleFunc("POST /v1/connect/redeem", s.handleConnectRedeem)

	mux.HandleFunc("GET /outcomes/cohorts", s.htmlView(s.pageInsightCohorts))
	mux.HandleFunc("GET /outcomes/cohorts/{key}", s.htmlView(s.pageInsightCohortDetail))
	mux.HandleFunc("GET /outcomes/dismissals/{reason}", s.htmlView(s.pageInsightDismissals))
	mux.HandleFunc("GET /outcomes/source/{provenance}", s.htmlView(s.pageInsightSource))
	mux.HandleFunc("GET /outcomes/tag/{tag}", s.htmlView(s.pageInsightTag))
	mux.HandleFunc("GET /outcomes/task-type/{type}", s.htmlView(s.pageInsightTaskType))
	mux.HandleFunc("GET /outcomes/helped-rate", s.htmlView(s.pageInsightHelpedRate))
	mux.HandleFunc("GET /outcomes/signal-trust", s.htmlView(s.pageInsightSignalTrust))
	mux.HandleFunc("GET /outcomes/{id...}", s.htmlView(s.pageInsightDetail))

	mux.HandleFunc("GET /techniques", s.htmlView(s.pageIndex))
	mux.HandleFunc("GET /techniques/retired", s.htmlView(s.pageIndex))
	mux.HandleFunc("GET /techniques/map", s.htmlView(s.pageTechniqueMap))
	mux.HandleFunc("POST /techniques/map/describe", s.adminOnly(s.handleClusterDescribe))
	mux.HandleFunc("GET /techniques/tags", s.htmlView(s.pageTags))
	mux.HandleFunc("POST /admin/tags/rename", s.adminOnly(s.handleTagRename))
	mux.HandleFunc("POST /admin/tags/delete", s.adminOnly(s.handleTagDelete))
	mux.HandleFunc("POST /admin/tags/propose", s.adminOnly(s.handleTagPropose))
	mux.HandleFunc("POST /admin/tags/apply", s.adminOnly(s.handleTagApply))
	mux.HandleFunc("POST /admin/tags/discard", s.adminOnly(s.handleTagDiscard))
	mux.HandleFunc("GET /techniques/history/{id...}", s.htmlView(s.pageTechniqueHistory))
	mux.HandleFunc("GET /techniques/{id...}", s.htmlView(s.pageTechniqueDetail))

	mux.HandleFunc("GET /review", s.htmlView(s.pageReview))
	mux.HandleFunc("GET /drafts/{id...}", s.htmlView(s.pageDraftDetail))
	mux.HandleFunc("GET /docs", s.htmlView(s.pageDocs))
	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("GET /settings/models", s.handleSettingsModels)
	mux.HandleFunc("POST /settings/signin/test", s.handleSignInTest)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)
	mux.HandleFunc("GET /federation", s.htmlView(s.pageFederation))
	mux.HandleFunc("GET /docs/{slug...}", s.docsAssetOr(s.publicHTMLView(docsRequestPublic, s.pageDocs)))
}
