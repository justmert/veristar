package operator

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// request counts whose range sum passes the 64-bit limit: one tenant's
// numbers saturate at the limit and the list, the compare and every other
// tenant's row still answer, instead of the whole query failing with
// 22003 numeric value out of range.
func TestRequestSumsPastInt64Saturate(t *testing.T) {
	d, pg := reviewDB(t)
	g, err := NewGlobal(d)
	require.NoError(t, err)
	r := chi.NewRouter()
	r.Route("/api/operator", g.Routes)

	big, small := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	superExec(t, pg, `insert into cp.tenants (id, slug, name, status) values ($1, 'big', 'big', 'active'), ($2, 'small', 'small', 'active')`, big, small)
	today := utcDay(time.Now())
	from := today.AddDate(0, 0, -2)
	bigCounts, smallCounts := make([]int64, 15), make([]int64, 15)
	bigCounts[3], smallCounts[3] = 6000000000000000000, 10
	// two days of 6e18 requests each: every day fits, the range does not
	for i := range 2 {
		day := from.AddDate(0, 0, i)
		superExec(t, pg, `insert into api.metrics_daily (tenant_id, bucket_start, service, method, route, status_code, source, counts, sum, count, lossy)
			values ($1, $2, 'svc', 'GET', '/x', 500, 'metric', $3, 1, 6000000000000000000, false),
			       ($4, $2, 'svc', 'GET', '/x', 200, 'metric', $5, 1, 10, false)`, big, day, bigCounts, small, smallCounts)
	}
	q := "from=" + from.Format(time.DateOnly) + "&to=" + today.Format(time.DateOnly)
	get := func(path string) (int, []byte) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code, w.Body.Bytes()
	}

	code, body := get("/api/operator/api/projects?" + q + "&limit=200")
	require.Equal(t, http.StatusOK, code, string(body))
	rows := map[uuid.UUID]APIProject{}
	for _, p := range decode[GlobalList[APIProject]](t, body).Items {
		rows[p.Tenant.ID] = p
	}
	require.Equal(t, int64(math.MaxInt64), rows[big].Requests)
	require.Equal(t, int64(math.MaxInt64), rows[big].Requests5xx)
	require.Equal(t, int64(20), rows[small].Requests)
	// every request of both days sits in the bucket 0.025 to 0.05 s
	require.NotNil(t, rows[big].P50Seconds)
	require.InDelta(t, 0.0375, *rows[big].P50Seconds, 0.0125)

	code, body = get("/api/operator/web/projects?" + q + "&limit=200")
	require.Equal(t, http.StatusOK, code, string(body))

	code, body = get("/api/operator/api/compare?" + q + "&tenants=" + big.String() + "," + small.String())
	require.Equal(t, http.StatusOK, code, string(body))
	cmp := decode[APICompare](t, body)
	require.Equal(t, int64(math.MaxInt64), cmp.Projects[0].Totals.Requests)
	require.NotNil(t, cmp.Projects[0].Totals.P50Seconds)
}
