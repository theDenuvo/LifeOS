-- +goose Up
-- +goose StatementBegin
-- Which tools of an MCP server may be used without a write is the user's
-- decision, recorded per tool: the server's own annotation is only the server
-- describing itself. The list is stored as a JSON array of tool names.
ALTER TABLE research_connections ADD COLUMN allowed_tools TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE research_connections DROP COLUMN allowed_tools;
-- +goose StatementEnd
