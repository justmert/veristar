package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// the umami report tools: funnels, journeys, retention and goals. saved
// funnel and goal ids are checked against the site's own list before use,
// besides umami's own check that a report belongs to the website.

type listIn struct {
	siteIn
	Search   string `json:"search,omitempty" jsonschema:"only reports whose name or description contains this text"`
	Page     int    `json:"page,omitempty" jsonschema:"page, from 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"reports per page, 1 to 100, default 20"`
}

func listPatch(props map[string]*jsonschema.Schema) {
	sitePatch(props)
	maxLen(props, "search", 200)
	intRange(props, "page", 1, maxPage)
	intRange(props, "page_size", 1, maxPageSize)
}

func (in listIn) query() (url.Values, int, int, error) {
	page, size := orDefault(in.Page, 1), orDefault(in.PageSize, defaultPageSize)
	if err := checkInt("page", page, 1, maxPage); err != nil {
		return nil, 0, 0, err
	}
	if err := checkInt("page_size", size, 1, maxPageSize); err != nil {
		return nil, 0, 0, err
	}
	if err := text("search", in.Search, 200); err != nil {
		return nil, 0, 0, err
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(size))
	if in.Search != "" {
		q.Set("search", in.Search)
	}
	return q, page, size, nil
}

// savedReport is the part of a umami saved report the tools return.
type savedReport struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	CreatedAt   string          `json:"createdAt"`
}

type reportPage struct {
	Data  []savedReport `json:"data"`
	Count flexNumber    `json:"count"`
}

// maxListedReports bounds the search for a saved report id: umami's own
// ui shows far fewer per site
const maxListedReports = 1000

// hasReport checks that id is a saved report of the site, by listing them.
func (s *Server) hasReport(ctx context.Context, p Principal, w website, kind, id string) error {
	q := url.Values{}
	q.Set("page", "1")
	q.Set("pageSize", strconv.Itoa(maxListedReports))
	var page reportPage
	if err := s.umamiRead(ctx, p, w, "/"+kind, q, &page); err != nil {
		return err
	}
	for _, r := range page.Data {
		if r.ID == id {
			return nil
		}
	}
	return &toolError{Code: outcomeNotFound, Message: fmt.Sprintf("no saved %s with this id belongs to the site; list them with list_%s", kind[:len(kind)-1], kind)}
}

type funnelStep struct {
	Type  string `json:"type" jsonschema:"path for a page path, event for a custom event name"`
	Value string `json:"value" jsonschema:"the page path (/pricing) or event name (signup); * is a wildcard"`
}

func (s *Server) addReportTools(srv *mcp.Server) {
	addTool(s, srv, &mcp.Tool{
		Name:  "list_funnels",
		Title: "Saved funnels",
		Description: "Lists the funnels saved for a site in Umami, with each funnel's steps and window. When the " +
			"user names a funnel (\"the checkout funnel\"), find its id here and pass it to run_funnel. Needs a " +
			"site_id from list_sites.",
	}, listPatch, func(ctx context.Context, p Principal, in listIn) (any, error) {
		q, page, size, err := in.query()
		if err != nil {
			return nil, err
		}
		w, err := s.resolveSite(ctx, p, in.SiteID, in.View)
		if err != nil {
			return nil, err
		}
		var raw reportPage
		if err := s.umamiRead(ctx, p, w, "/funnels", q, &raw); err != nil {
			return nil, err
		}
		if raw.Data == nil {
			raw.Data = []savedReport{}
		}
		return map[string]any{"site": w.out(), "page": page, "page_size": size, "total": int64(raw.Count),
			"has_more": int64(page*size) < int64(raw.Count), "funnels": raw.Data}, nil
	})

	type funnelIn struct {
		siteIn
		rangeIn
		FunnelID      string       `json:"funnel_id,omitempty" jsonschema:"id of a saved funnel from list_funnels; its steps and window are used"`
		Steps         []funnelStep `json:"steps,omitempty" jsonschema:"2 to 8 ordered steps, when no funnel_id is given"`
		WindowMinutes int          `json:"window_minutes,omitempty" jsonschema:"minutes allowed from the first step to each later one, 1 to 43200, default 60; ignored with funnel_id"`
		Filters       *webFilters  `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "run_funnel",
		Title: "Funnel conversion",
		Description: "How many visitors of a site went through an ordered sequence of 2 to 8 steps (page paths " +
			"or custom events) within a time window, and where they dropped off. Pass funnel_id for a saved funnel " +
			"(list_funnels) or define steps. Use it for questions such as \"how many visitors who saw /pricing " +
			"signed up?\". Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		uuidFormat(props, "funnel_id")
		lo, hi := 2, 8
		props["steps"].MinItems, props["steps"].MaxItems = &lo, &hi
		enum(props["steps"].Items.Properties, "type", "path", "event")
		maxLen(props["steps"].Items.Properties, "value", 500)
		intRange(props, "window_minutes", 1, 43200)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in funnelIn) (any, error) {
		if (in.FunnelID == "") == (len(in.Steps) == 0) {
			return nil, invalid("give either funnel_id or steps")
		}
		var steps []byte
		window := orDefault(in.WindowMinutes, 60)
		if in.FunnelID != "" {
			if _, err := parseUUID("funnel_id", in.FunnelID); err != nil {
				return nil, err
			}
		} else {
			if len(in.Steps) < 2 || len(in.Steps) > 8 {
				return nil, invalid("steps must hold 2 to 8 steps")
			}
			for i, st := range in.Steps {
				if st.Type != "path" && st.Type != "event" {
					return nil, invalid("steps[%d].type must be path or event", i)
				}
				if st.Value == "" {
					return nil, invalid("steps[%d].value is required", i)
				}
				if err := text(fmt.Sprintf("steps[%d].value", i), st.Value, 500); err != nil {
					return nil, err
				}
			}
			if err := checkInt("window_minutes", window, 1, 43200); err != nil {
				return nil, err
			}
			var err error
			if steps, err = json.Marshal(in.Steps); err != nil {
				return nil, err
			}
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"site": w.out(), "range": rng}
		var raw json.RawMessage
		if in.FunnelID != "" {
			if err := s.hasReport(ctx, p, w, "funnels", in.FunnelID); err != nil {
				return nil, err
			}
			if err := s.umamiRead(ctx, p, w, "/funnels/"+in.FunnelID+"/stats", q, &raw); err != nil {
				return nil, err
			}
			out["funnel_id"] = in.FunnelID
		} else {
			q.Set("steps", string(steps))
			q.Set("window", strconv.Itoa(window))
			if err := s.umamiRead(ctx, p, w, "/funnels/stats", q, &raw); err != nil {
				return nil, err
			}
			out["window_minutes"] = window
		}
		out["steps"] = raw
		out["note"] = "per step: visitors reaching it, previous step's visitors, dropped, dropoff and remaining as fractions"
		return out, nil
	})

	type journeyIn struct {
		siteIn
		rangeIn
		Steps     int         `json:"steps,omitempty" jsonschema:"steps per journey, 2 to 7, default 3"`
		StartStep string      `json:"start_step,omitempty" jsonschema:"only journeys starting with this page path or event"`
		EndStep   string      `json:"end_step,omitempty" jsonschema:"only journeys ending with this page path or event"`
		Filters   *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "run_journey",
		Title: "Visitor journeys",
		Description: "The most common paths visitors take through a site, as sequences of 2 to 7 pages or " +
			"events with the number of visitors on each, optionally anchored on a start or end step. Use it for " +
			"\"what do visitors do after the home page?\". Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		intRange(props, "steps", 2, 7)
		maxLen(props, "start_step", 500)
		maxLen(props, "end_step", 500)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in journeyIn) (any, error) {
		steps := orDefault(in.Steps, 3)
		if err := checkInt("steps", steps, 2, 7); err != nil {
			return nil, err
		}
		for name, v := range map[string]string{"start_step": in.StartStep, "end_step": in.EndStep} {
			if err := text(name, v, 500); err != nil {
				return nil, err
			}
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		q.Set("steps", strconv.Itoa(steps))
		if in.StartStep != "" {
			q.Set("startStep", in.StartStep)
		}
		if in.EndStep != "" {
			q.Set("endStep", in.EndStep)
		}
		var raw json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/journeys", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "steps": steps, "journeys": raw}, nil
	})

	type retentionIn struct {
		siteIn
		rangeIn
		Timezone string      `json:"timezone,omitempty" jsonschema:"IANA zone for the cohort days, default UTC"`
		Filters  *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "run_retention",
		Title: "Visitor retention",
		Description: "A cohort retention table for a site: for each day visitors first came, how many returned " +
			"on each later day and the share. Visitor ids rotate daily for privacy, so returns are counted within " +
			"what Umami can link and are a lower bound. Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		sitePatch(props)
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in retentionIn) (any, error) {
		tz, err := checkTimezone(in.Timezone)
		if err != nil {
			return nil, err
		}
		w, q, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		q.Set("timezone", tz)
		var raw json.RawMessage
		if err := s.umamiRead(ctx, p, w, "/retention", q, &raw); err != nil {
			return nil, err
		}
		return map[string]any{"site": w.out(), "range": rng, "timezone": tz, "cohorts": raw}, nil
	})

	type goalsIn struct {
		listIn
		rangeIn
		GoalID      string      `json:"goal_id,omitempty" jsonschema:"only this goal"`
		WithResults bool        `json:"with_results,omitempty" jsonschema:"also compute each goal's conversions in the range (from, to)"`
		Filters     *webFilters `json:"filters,omitempty"`
	}
	addTool(s, srv, &mcp.Tool{
		Name:  "get_goals",
		Title: "Saved goals",
		Description: "Lists the goals saved for a site in Umami (a target page path or custom event). With " +
			"with_results it also computes, for the range, each goal's conversions, the visitors and the conversion " +
			"rate. Use it for \"how are we doing against our goals?\". Needs a site_id from list_sites.",
	}, func(props map[string]*jsonschema.Schema) {
		listPatch(props)
		uuidFormat(props, "goal_id")
		props["filters"].Description = filterHelp
	}, func(ctx context.Context, p Principal, in goalsIn) (any, error) {
		q, page, size, err := in.query()
		if err != nil {
			return nil, err
		}
		if in.GoalID != "" {
			if _, err := parseUUID("goal_id", in.GoalID); err != nil {
				return nil, err
			}
		}
		w, sq, rng, err := s.webCall(ctx, p, in.siteIn, in.rangeIn, in.Filters, true)
		if err != nil {
			return nil, err
		}
		var raw reportPage
		if err := s.umamiRead(ctx, p, w, "/goals", q, &raw); err != nil {
			return nil, err
		}
		type goal struct {
			savedReport
			Results json.RawMessage `json:"results,omitempty"`
		}
		goals := []goal{}
		for _, r := range raw.Data {
			if in.GoalID != "" && r.ID != in.GoalID {
				continue
			}
			g := goal{savedReport: r}
			if in.WithResults {
				if _, err := parseUUID("goal id", r.ID); err != nil {
					return nil, fmt.Errorf("mcpserver: umami goal id %q", r.ID)
				}
				if err := s.umamiRead(ctx, p, w, "/goals/"+r.ID+"/stats", sq, &g.Results); err != nil {
					return nil, err
				}
			}
			goals = append(goals, g)
		}
		out := map[string]any{"site": w.out(), "page": page, "page_size": size, "total": int64(raw.Count),
			"has_more": int64(page*size) < int64(raw.Count), "goals": goals}
		if in.WithResults {
			out["range"] = rng
			out["note"] = "results: num is the visitors who reached the goal, total the visitors in the range"
		}
		return out, nil
	})
}
