-- fencing for singleton workers, used by metrics_writer and jobs
-- inside every write transaction, through natsx.CheckFence.

-- name: AdvanceFence :execrows
-- the token is (epoch, revision): a newer epoch wins whatever its revision,
-- because the memory-backed lock revisions restart after a full nats cluster
-- restart; inside one epoch the revision must not go back. zero rows means a
-- newer token is stored, so the caller lost its lock and must roll back. the
-- row lock taken here is held until commit, so a former leader racing a new
-- one waits and then sees the new token.
update cp.leader_fence
set epoch = @epoch, revision = @revision
where name = @name
  and (epoch < @epoch or (epoch = @epoch and revision <= @revision));

-- name: GetFence :one
select epoch, revision from cp.leader_fence where name = @name;
