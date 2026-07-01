-- name: InsertWebappAuditLog :exec
INSERT INTO webapp.audit_log (user_id, action, ip_address, user_agent, metadata)
VALUES ($1, $2, $3, $4, $5);
