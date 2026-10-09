-- Every service database gets this function. The CDC sync daemon tracks
-- changes by updated_at, so every UPDATE must move it, including raw SQL
-- that bypasses GORM.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION set_updated_at();
