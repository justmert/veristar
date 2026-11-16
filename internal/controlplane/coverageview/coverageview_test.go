package coverageview

import (
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/justmert/veristar/internal/coverage"
)

func TestShareSQLUsesTheMinimum(t *testing.T) {
	if !strings.Contains(ShareSQL, ">= "+strconv.Itoa(coverage.MinRequests)+" ") {
		t.Fatalf("ShareSQL does not use coverage.MinRequests (%d): %s", coverage.MinRequests, ShareSQL)
	}
}

func TestEstimate(t *testing.T) {
	if Of(99, 10).UnmeasuredShare != nil {
		t.Fatal("a share below the minimum")
	}
	e := Of(200, 30)
	if e.UnmeasuredShare == nil || *e.UnmeasuredShare != 0.15 {
		t.Fatalf("share %v, want 0.15", e.UnmeasuredShare)
	}
	if Of(100, 101).UnmeasuredShare != nil {
		t.Fatal("a share above one while gpc leads its requests")
	}
	tn, a, b := uuid.New(), uuid.New(), uuid.New()
	c := Counts{
		{Tenant: tn, Site: a, Day: "2031-01-01"}:         Of(60, 6),
		{Tenant: tn, Site: a, Day: "2031-01-02"}:         Of(60, 12),
		{Tenant: tn, Site: b, Day: "2031-01-01"}:         Of(80, 2),
		{Tenant: uuid.New(), Site: b, Day: "2031-01-01"}: Of(1000, 900),
	}
	if got := c.Site(tn, a); got.ScriptRequests != 120 || got.GPCRequests != 18 || *got.UnmeasuredShare != 0.15 {
		t.Fatalf("site sum %+v", got)
	}
	if got := c.Tenant(tn); got.ScriptRequests != 200 || got.GPCRequests != 20 {
		t.Fatalf("tenant sum %+v", got)
	}
	if got := c.TenantDay(tn, "2031-01-01"); got.ScriptRequests != 140 || got.UnmeasuredShare == nil {
		t.Fatalf("tenant day %+v", got)
	}
	if len(c.Sites(tn)) != 2 {
		t.Fatal("two sites")
	}
}
