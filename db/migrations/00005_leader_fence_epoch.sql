-- epoch-aware fencing tokens.
--
-- the revision of a lock in the memory-backed locks bucket restarts at 1
-- after a full nats cluster restart, so a revision alone would refuse every
-- new leader once a higher pre-restart revision is stored. the token becomes
-- (epoch, revision): epoch is the server time, in unix nanoseconds, of the
-- message that acquired the lock, so it is fixed for one hold and grows with
-- every new hold, whatever the revision does. the stream's creation time does
-- not work as the epoch: a 3-node cluster keeps a memory stream's
-- configuration and creation time across a full restart while its sequence
-- starts again from 0 (observed on nats 2.15.3).
--
-- a write is accepted when its epoch is newer than the stored one, or equal
-- with a revision at least as high. epoch 0 is older than any real epoch, so
-- the first leader always wins.

-- +goose Up
alter table cp.leader_fence add column epoch bigint not null default 0 check (epoch >= 0);

-- +goose StatementBegin
create or replace function cp.leader_fence_monotonic() returns trigger
language plpgsql
as $$
begin
  if new.name <> old.name then
    raise exception 'cp.leader_fence: name of fence % cannot change', old.name
      using errcode = 'check_violation';
  end if;
  if new.epoch < old.epoch then
    raise exception 'cp.leader_fence: epoch % for % is older than stored epoch %',
      new.epoch, old.name, old.epoch
      using errcode = 'check_violation';
  end if;
  -- a new epoch may start again at a low revision; within one epoch the
  -- revision never goes back
  if new.epoch = old.epoch and new.revision < old.revision then
    raise exception 'cp.leader_fence: revision % for % is older than stored revision %',
      new.revision, old.name, old.revision
      using errcode = 'check_violation';
  end if;
  return new;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
create or replace function cp.leader_fence_monotonic() returns trigger
language plpgsql
as $$
begin
  if new.name <> old.name then
    raise exception 'cp.leader_fence: name of fence % cannot change', old.name
      using errcode = 'check_violation';
  end if;
  if new.revision < old.revision then
    raise exception 'cp.leader_fence: revision % for % is older than stored revision %',
      new.revision, old.name, old.revision
      using errcode = 'check_violation';
  end if;
  return new;
end
$$;
-- +goose StatementEnd
alter table cp.leader_fence drop column epoch;
