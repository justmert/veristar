-- cp_operator loses its read right on the scf award directory (00050).
-- nothing running as cp_operator reads cp.scf_awards or
-- cp.scf_award_imports: the enrollment search, the award picked at
-- enrollment and the matching all run under cp_system, and the operator
-- sees an award only as the cohort proposal it became. a right nobody uses
-- is one more door a mistake in an operator query could open.

-- +goose Up
revoke select on cp.scf_award_imports, cp.scf_awards from cp_operator;

-- +goose Down
grant select on cp.scf_award_imports, cp.scf_awards to cp_operator;
