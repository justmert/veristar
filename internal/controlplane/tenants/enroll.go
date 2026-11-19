package tenants

import (
	"encoding/json"
	"time"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// EnrollmentInput is the enrollment object of a new project (contract
// 3.2), for callers outside the routes such as platformctl tenant create.
type EnrollmentInput = enrollment

// StoredEnrollment is the checked enrollment object as enrollment_answers
// holds it.
type StoredEnrollment = storedEnrollment

// NewTenantInput is what a new project names: the fields of POST
// /api/tenants other than the terms version and the scf award.
type NewTenantInput struct {
	Name        *string
	Slug        *string
	Description *string
	Enrollment  *EnrollmentInput
	Cohort      *CohortInput
}

// NewTenant is a checked NewTenantInput, as it is stored.
type NewTenant struct {
	Name        string
	Slug        string
	Description string
	Enrollment  StoredEnrollment
	// Cohort is empty without a cohort object
	Cohort CohortValues
}

// Answers is the enrollment as the enrollment_answers column holds it.
func (n NewTenant) Answers() ([]byte, error) { return json.Marshal(n.Enrollment) }

// CheckNewTenant validates a new project the way POST /api/tenants does and
// adds every problem to f under the request's json paths. platformctl uses
// it too, so both enrollments accept exactly the same values.
func CheckNewTenant(f *Fields, in NewTenantInput, today time.Time) NewTenant {
	var out NewTenant
	if in.Name == nil {
		f.Add("name", httpx.FieldRequired)
	} else {
		out.Name = Text(f, "name", *in.Name, 1, 100)
	}
	if in.Description != nil {
		out.Description = Text(f, "description", *in.Description, 0, 1000)
	}
	if in.Slug != nil {
		out.Slug = *in.Slug
		if !slugRe.MatchString(out.Slug) {
			f.Add("slug", httpx.FieldInvalidFormat)
		}
	} else if in.Name != nil {
		if out.Slug = DeriveSlug(out.Name); out.Slug == "" {
			// nothing in the name maps to [a-z0-9]; the client must choose one
			f.Add("slug", httpx.FieldRequired)
		}
	}
	out.Enrollment = checkEnrollment(f, in.Enrollment)
	if in.Cohort != nil {
		out.Cohort = CheckCohort(f, "cohort", in.Cohort, today, false)
	}
	return out
}
