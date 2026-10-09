-- Local `go run` development only (docker compose). In kind, each service's
-- Helm release declares its database as a CloudNativePG Database resource.
CREATE DATABASE customers OWNER demo;
CREATE DATABASE marketing OWNER demo;
CREATE DATABASE inventory OWNER demo;
CREATE DATABASE payments OWNER demo;
CREATE DATABASE shipments OWNER demo;
CREATE DATABASE orders OWNER demo;
