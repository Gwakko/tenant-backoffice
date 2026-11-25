-- +goose Up
CREATE TABLE fail_first (id int PRIMARY KEY);
SELECT 1 / 0;

-- +goose Down
DROP TABLE fail_first;
