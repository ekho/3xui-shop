-- name: MaintenanceStatus :one
SELECT enabled, revision, changed_at FROM maintenance_state WHERE singleton = true;
