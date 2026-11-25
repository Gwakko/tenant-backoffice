-- +goose Up
CREATE TABLE fail_ok (id int PRIMARY KEY);

-- +goose Down
DROP TABLE fail_ok;
