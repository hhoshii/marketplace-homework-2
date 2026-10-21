#!/bin/sh
set -eu

# Runs once on an empty PostgreSQL volume. Table schemas are managed by Flyway.
psql -U postgres -d postgres -v ON_ERROR_STOP=1 \
  -v users_password="$USERS_DB_PASSWORD" \
  -v commerce_password="$COMMERCE_DB_PASSWORD" <<'SQL'
CREATE ROLE users LOGIN PASSWORD :'users_password';
CREATE ROLE commerce LOGIN PASSWORD :'commerce_password';
CREATE DATABASE users OWNER users;
CREATE DATABASE commerce OWNER commerce;
REVOKE ALL ON DATABASE users FROM PUBLIC;
REVOKE ALL ON DATABASE commerce FROM PUBLIC;
SQL
