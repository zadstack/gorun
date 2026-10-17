-- These permissions remain owner-scoped in every workagents operation.
-- Profile/schedule management is not granted to customer/editorial roles.
INSERT OR IGNORE INTO role_permissions(role,permission)
SELECT slug,'workagents.read' FROM roles WHERE slug IN ('customer','editor','author','contributor');
INSERT OR IGNORE INTO role_permissions(role,permission)
SELECT slug,'workagents.run' FROM roles WHERE slug IN ('customer','editor','author','contributor');
