package integrity

import (
	"strings"
	"testing"
)

func ptr(v int64) *int64 { return &v }

// clean is a day that no rule lowers: mostly access networks, a normal
// spread of networks, no quarantine, a steady baseline.
func clean() Inputs {
	return Inputs{
		Events: 1000, Quarantined: 0, ProofFailures: 10,
		NetAccess: 850, NetOrganisation: 60, NetHosting: 80, NetAnonymiser: 0, NetUnknown: 10,
		Networks: 120, Visitors: 400, Pageviews: 1000,
		BaselineVisitors: ptr(380), BaselineDaysUsed: 28,
	}
}

func codes(r Result) []string {
	var out []string
	for _, x := range r.Reasons {
		out = append(out, x.Code+":"+x.Level)
	}
	return out
}

func TestGradeRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*Inputs)
		grade string
		want  []string
	}{
		{"clean day is A", func(*Inputs) {}, GradeA, nil},
		{"too little traffic", func(in *Inputs) { in.Events = 49 }, "", []string{"too_little_traffic:none"}},
		{"hosting share B", func(in *Inputs) { in.NetAccess, in.NetHosting = 650, 280 }, GradeB, []string{"hosting_share:B"}},
		{"anonymiser counts with hosting", func(in *Inputs) { in.NetAccess, in.NetHosting, in.NetAnonymiser = 400, 300, 230 }, GradeC, []string{"hosting_share:C"}},
		{"quarantine share B", func(in *Inputs) { in.Quarantined = 100 }, GradeB, []string{"quarantine_share:B"}},
		{"quarantine share C", func(in *Inputs) { in.Quarantined = 300 }, GradeC, []string{"quarantine_share:C"}},
		{"few networks", func(in *Inputs) { in.Networks = 3 }, GradeB, []string{"few_networks:B"}},
		{"few networks ignored before classification", func(in *Inputs) {
			in.NetAccess, in.NetOrganisation, in.NetHosting, in.NetUnknown, in.Networks = 0, 0, 0, 0, 0
		}, GradeA, nil},
		{"spike", func(in *Inputs) { in.BaselineVisitors = ptr(30) }, GradeB, []string{"spike:B"}},
		{"spike needs a week of baseline", func(in *Inputs) { in.BaselineVisitors, in.BaselineDaysUsed = ptr(30), 6 }, GradeA, nil},
		{"proof failures", func(in *Inputs) { in.ProofFailures = 1000 }, GradeB, []string{"proof_failures:B"}},
		{"page reported volume lowers", func(in *Inputs) { in.Pageviews = 20000 }, GradeB, []string{"page_volume:B"}},
		{"worst level wins", func(in *Inputs) { in.Quarantined, in.Networks = 400, 2 }, GradeC, []string{"quarantine_share:C", "few_networks:B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := clean()
			tc.edit(&in)
			r := Grade(in)
			if r.Grade != tc.grade {
				t.Fatalf("grade %q, want %q (reasons %v)", r.Grade, tc.grade, codes(r))
			}
			if got := strings.Join(codes(r), ","); got != strings.Join(tc.want, ",") {
				t.Fatalf("reasons %q, want %q", got, strings.Join(tc.want, ","))
			}
			for _, x := range r.Reasons {
				if x.Text() == x.Code || strings.Contains(x.Text(), "%!") {
					t.Fatalf("reason %s has no proper text: %q", x.Code, x.Text())
				}
			}
		})
	}
}

// page-reported numbers can only lower: the cleanest page views leave a
// platform-observed C where it is.
func TestPageReportedNeverRaises(t *testing.T) {
	in := clean()
	in.NetAccess, in.NetHosting = 300, 630
	base := Grade(in).Grade
	for _, pv := range []int64{0, 1, 400, 1000, 5000} {
		in.Pageviews = pv
		if g := Grade(in).Grade; Rank(g) < Rank(base) {
			t.Fatalf("page views %d raised the grade from %s to %s", pv, base, g)
		}
	}
}

func TestCombine(t *testing.T) {
	hs := Reason{Code: ReasonHostingShare, Level: LevelB}
	hsC := Reason{Code: ReasonHostingShare, Level: LevelC}
	q := Reason{Code: ReasonQuarantineShare, Level: LevelB}
	days := []DayGrade{
		{Grade: GradeA}, {Grade: GradeA}, {Grade: GradeA}, {Grade: GradeA}, {Grade: GradeA}, {Grade: GradeA}, {Grade: GradeA},
		{Grade: GradeB, Reasons: []Reason{hs}},
		{Grade: GradeC, Reasons: []Reason{hsC, q}},
		{Reasons: []Reason{{Code: ReasonTooLittleTraffic, Level: LevelNone}}},
	}
	r := Combine(days)
	if r.Grade != GradeA || r.GradedDays != 9 || r.DaysB != 1 || r.DaysC != 1 {
		t.Fatalf("two bad days in nine: %+v", r)
	}
	if len(r.Reasons) != 2 || r.Reasons[0].Code != ReasonHostingShare || r.Reasons[0].Level != LevelC || r.Reasons[0].Days != 2 {
		t.Fatalf("reasons %+v", r.Reasons)
	}
	days = append(days, DayGrade{Grade: GradeC, Reasons: []Reason{hsC}})
	if g := Combine(days).Grade; g != GradeB {
		t.Fatalf("three of ten days B or worse: %s, want B", g)
	}
	if g := Combine(nil); g.Grade != "" || g.Reasons == nil {
		t.Fatalf("empty range %+v", g)
	}
	for _, tc := range []struct {
		a, b, c int
		want    string
	}{{0, 0, 0, ""}, {3, 0, 1, GradeC}, {4, 0, 1, GradeA}, {2, 1, 1, GradeC}, {6, 1, 1, GradeB}, {7, 1, 0, GradeA}} {
		if g := RangeOf(tc.a, tc.b, tc.c); g != tc.want {
			t.Fatalf("RangeOf(%d,%d,%d) = %q, want %q", tc.a, tc.b, tc.c, g, tc.want)
		}
	}
}

func TestPrefixThreshold(t *testing.T) {
	for _, tc := range []struct {
		class NetClass
		scale int
		want  int
	}{
		{NetAccess, 0, 20},
		{NetUnknown, 1024, 20},
		{NetAccess, 8192, 81},
		{NetOrganisation, 4096, 40},
		{NetHosting, 0, 5},
		{NetAnonymiser, 4096, 5},
		{NetHosting, 65536, 65},
		{NetAccess, 1 << 30, MaxPrefixThreshold},
	} {
		if got := PrefixThreshold(20, tc.class, tc.scale); got != tc.want {
			t.Fatalf("PrefixThreshold(20, %s, %d) = %d, want %d", tc.class, tc.scale, got, tc.want)
		}
	}
	if got := PrefixThreshold(1, NetHosting, 0); got != 1 {
		t.Fatalf("floor 1 hosting = %d", got)
	}
}

func TestScaleOf(t *testing.T) {
	for in, want := range map[int64]int{-1: 0, 0: 0, 1: 1, 2: 2, 3: 2, 1000: 512, 1024: 1024, 5000: 4096} {
		if got := ScaleOf(in); got != want {
			t.Fatalf("ScaleOf(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestOutcomes(t *testing.T) {
	for _, c := range NetClasses {
		got, ok := ParseEvidenceOutcome(EvidenceOutcome(c))
		if !ok || got != c {
			t.Fatalf("round trip of %s", c)
		}
	}
	for _, bad := range []string{"evidence_net_", "evidence_net_vpn", "accepted", "network_abc"} {
		if _, ok := ParseEvidenceOutcome(bad); ok {
			t.Fatalf("%q parsed as evidence", bad)
		}
	}
	if !IsNetworkOutcome("network_0123456789abcdef") || IsNetworkOutcome("network_0123456789ABCDEF") || IsNetworkOutcome("network_01") {
		t.Fatal("network outcome pattern")
	}
}

func TestRangeText(t *testing.T) {
	for _, c := range ReasonCodes {
		if RangeText(c) == c {
			t.Fatalf("no range text for %s", c)
		}
	}
}

// rule version 2 adds the page signals of tracker 1.3.0; they can only lower
// a grade, and version 1 ignores them, so a stored version 1 grade is
// reproduced exactly from its inputs.
func TestGradeSignalRules(t *testing.T) {
	signals := func(views, framed, nv int64) func(*Inputs) {
		return func(in *Inputs) { in.SignalViews, in.Framed, in.NeverVisible = views, framed, nv }
	}
	for _, tc := range []struct {
		name   string
		edit   func(*Inputs)
		v1, v2 string
		want2  []string
	}{
		{"no signals reported", func(*Inputs) {}, GradeA, GradeA, nil},
		{"few framed views stay A", signals(800, 100, 50), GradeA, GradeA, nil},
		{"framed share B", signals(800, 200, 0), GradeA, GradeB, []string{"framed_share:B"}},
		{"framed share C", signals(800, 700, 0), GradeA, GradeC, []string{"framed_share:C"}},
		{"framed share needs a base", signals(40, 40, 0), GradeA, GradeA, nil},
		{"never visible B", signals(600, 0, 300), GradeA, GradeB, []string{"never_visible_share:B"}},
		{"never visible below the limit", signals(800, 0, 200), GradeA, GradeA, nil},
		{"never visible needs a base", signals(10, 0, 30), GradeA, GradeA, nil},
		{"signals cannot raise a grade", func(in *Inputs) { in.Quarantined = 400; in.SignalViews = 900 }, GradeC, GradeC, []string{"quarantine_share:C"}},
		{"worst of both", func(in *Inputs) { in.Networks = 2; in.SignalViews, in.Framed = 800, 700 }, GradeB, GradeC, []string{"framed_share:C", "few_networks:B"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := clean()
			tc.edit(&in)
			r1, err := GradeVersion(1, in)
			if err != nil {
				t.Fatal(err)
			}
			if r1.Grade != tc.v1 {
				t.Fatalf("version 1 grade %q, want %q (%v)", r1.Grade, tc.v1, codes(r1))
			}
			for _, r := range r1.Reasons {
				if r.Code == ReasonFramedShare || r.Code == ReasonNeverVisibleShare {
					t.Fatalf("version 1 applied a version 2 rule: %v", codes(r1))
				}
			}
			r2, err := GradeVersion(2, in)
			if err != nil {
				t.Fatal(err)
			}
			if r2.Grade != tc.v2 {
				t.Fatalf("version 2 grade %q, want %q (%v)", r2.Grade, tc.v2, codes(r2))
			}
			got := map[string]bool{}
			for _, c := range codes(r2) {
				got[c] = true
			}
			for _, w := range tc.want2 {
				if !got[w] {
					t.Fatalf("version 2 reasons %v lack %s", codes(r2), w)
				}
			}
			if Rank(r2.Grade) < Rank(r1.Grade) {
				t.Fatalf("version 2 raised %s to %s", r1.Grade, r2.Grade)
			}
			for _, r := range r2.Reasons {
				if r.Text() == r.Code {
					t.Fatalf("reason %s has no text", r.Code)
				}
				if RangeText(r.Code) == r.Code {
					t.Fatalf("reason %s has no range text", r.Code)
				}
			}
		})
	}
}

func TestRuleVersions(t *testing.T) {
	if RuleVersions[len(RuleVersions)-1] != RuleVersion {
		t.Fatalf("RuleVersion %d is not the newest of %v", RuleVersion, RuleVersions)
	}
	if _, err := GradeVersion(0, clean()); err == nil {
		t.Fatal("version 0 graded")
	}
	if _, err := GradeVersion(RuleVersion+1, clean()); err == nil {
		t.Fatal("a future version graded")
	}
	if Grade(clean()).Grade != GradeA {
		t.Fatal("Grade does not apply the current version")
	}
}

func TestReportsSignals(t *testing.T) {
	for v, want := range map[string]bool{"": false, "1.0.0": false, "1.2.0": false, "1.2.9": false,
		"1.3.0": true, "1.3.1": true, "1.10.0": true, "2.0.0": true, "x.y.z": false, "1.3": false} {
		if got := ReportsSignals(v); got != want {
			t.Errorf("ReportsSignals(%q) = %v, want %v", v, got, want)
		}
	}
}
