-- name: InsertAuditLog :exec
INSERT INTO audit_log (user_id, action, ip_address, user_agent, metadata)
VALUES ($1, $2, $3, $4, $5);
