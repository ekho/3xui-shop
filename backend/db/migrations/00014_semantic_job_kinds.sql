-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
 job record;
 candidate_count integer;
 classified_kind text;
BEGIN
 -- Goose runs before River creates its tables on a fresh database.
 IF to_regclass('river_job') IS NULL THEN
  RETURN;
 END IF;

 -- This application has never requested River uniqueness for these jobs.
 -- A non-null hash may include the old kind, so its semantics need review.
 IF EXISTS (SELECT 1 FROM river_job WHERE unique_key IS NOT NULL) THEN
  RAISE EXCEPTION 'job kind migration blocked: unique River key';
 END IF;

 FOR job IN SELECT id,args,queue FROM river_job ORDER BY id LOOP
  SELECT count(*),min(kind) INTO candidate_count,classified_kind
  FROM (VALUES
   ('mail_delivery', job.queue='default' AND jsonb_typeof(job.args)='object'
    AND jsonb_typeof(job.args->'delivery_id')='string'
    AND job.args - 'delivery_id'='{}'::jsonb
    AND EXISTS (SELECT 1 FROM mail_deliveries WHERE id::text=job.args->>'delivery_id')),
   ('trial_provision', job.queue='provision' AND jsonb_typeof(job.args)='object'
    AND jsonb_typeof(job.args->'operation_id')='string'
    AND job.args - 'operation_id'='{}'::jsonb
    AND EXISTS (SELECT 1 FROM trial_operations WHERE id::text=job.args->>'operation_id')),
   ('access_operation', job.queue='provision' AND jsonb_typeof(job.args)='object'
    AND jsonb_typeof(job.args->'operation_id')='string'
    AND job.args - 'operation_id'='{}'::jsonb
    AND EXISTS (SELECT 1 FROM access_operations WHERE id::text=job.args->>'operation_id')),
   ('monthly_traffic_reset', job.queue='provision' AND jsonb_typeof(job.args)='object'
    AND jsonb_typeof(job.args->'account_id')='string'
    AND jsonb_typeof(job.args->'local_period')='string'
    AND job.args - 'account_id' - 'local_period'='{}'::jsonb
    AND EXISTS (SELECT 1 FROM monthly_reset_periods
     WHERE account_id::text=job.args->>'account_id' AND local_period=job.args->>'local_period'))
  ) AS candidates(kind,matches) WHERE matches;
  IF candidate_count <> 1 THEN
   RAISE EXCEPTION 'job kind migration blocked: unclassified or ambiguous River job';
  END IF;
  UPDATE river_job SET kind=classified_kind WHERE id=job.id AND kind<>classified_kind;
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF to_regclass('river_job') IS NOT NULL THEN
  IF EXISTS (SELECT 1 FROM river_job) THEN
   RAISE EXCEPTION 'job kind downgrade blocked: retained River jobs';
  END IF;
 END IF;
END $$;
-- +goose StatementEnd
