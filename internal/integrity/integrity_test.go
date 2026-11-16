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
