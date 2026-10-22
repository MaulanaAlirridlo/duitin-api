# Duitin API

## Database and configuration

The initial PostgreSQL schema is in [`schema.sql`](./schema.sql). It creates `users`, `categories`, and `transactions` with per-user ownership. Apply it to a fresh database before starting the API.

Set `DATABASE_URL` (or the `PGHOST`, `PGPORT`, `PGUSER`, and `PGPASSWORD` variables) and `JWT_SECRET` in the environment or `.env` file. `JWT_SECRET` must contain at least 32 bytes. The API exits during startup if it is missing or too short.

## Authentication

- `POST /auth/register` accepts `name`, `email`, and `password` and creates a user. Passwords must be 8–72 bytes.
- `POST /auth/login` accepts `email` and `password` and returns a JWT valid for 24 hours.
- `GET /users/me` returns the authenticated user's public profile.

Include the login JWT in the `Authorization` header for every `/categories`, `/transactions`, and `/dashboard` request. These endpoints only access records owned by that token's user.
