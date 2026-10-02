-- name: CatalogueVisible :many
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision
WHERE NOT p.archived AND NOT p.current_hidden ORDER BY p.current_devices,p.id;

-- name: CatalogueOperatorPage :many
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision
ORDER BY p.current_devices,p.id LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::bigint;

-- name: CatalogueOperatorCount :one
SELECT count(*) FROM catalogue_plans;

-- name: CatalogueByID :one
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,p.current_devices,p.current_profile,p.current_hidden,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.id=$1;

-- name: CatalogueByIDForUpdate :one
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,p.current_devices,p.current_profile,p.current_hidden,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.id=$1 FOR UPDATE OF p;

-- name: CatalogueByLegacyID :one
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,p.current_devices,p.current_profile,p.current_hidden,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.legacy_plan_id=$1;

-- name: CatalogueUnlimited :many
SELECT p.id,p.legacy_plan_id,p.current_revision,p.archived,p.current_devices,p.current_profile,p.current_hidden,r.terms,r.actor_account_id,r.source,r.changed_at,r.reason
FROM catalogue_plans p JOIN catalogue_revisions r ON r.plan_id=p.id AND r.revision=p.current_revision WHERE p.current_profile='unlimited';

-- name: CatalogueVisibleCount :one
SELECT count(*) FROM catalogue_plans WHERE NOT archived AND NOT current_hidden;

-- name: InsertCataloguePlan :exec
INSERT INTO catalogue_plans(id,legacy_plan_id,current_revision,current_devices,current_profile,current_hidden,archived)
VALUES($1,$2,$3,$4,$5,$6,$7);

-- name: InsertCatalogueRevision :exec
INSERT INTO catalogue_revisions(plan_id,revision,terms,archived,actor_account_id,source,changed_at,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);

-- name: SetCatalogueCurrent :exec
UPDATE catalogue_plans SET current_revision=$2,current_devices=$3,current_profile=$4,current_hidden=$5,archived=$6 WHERE id=$1;
