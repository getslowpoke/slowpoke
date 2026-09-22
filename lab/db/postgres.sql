-- Same shop as MariaDB, same forgotten indexes.
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

CREATE TABLE customers (
  id SERIAL PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  email VARCHAR(150) NOT NULL,
  city VARCHAR(80) NOT NULL,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE products (
  id SERIAL PRIMARY KEY,
  name VARCHAR(150) NOT NULL,
  description TEXT NOT NULL,
  price NUMERIC(10,2) NOT NULL
);

CREATE TABLE orders (
  id SERIAL PRIMARY KEY,
  customer_id INT NOT NULL,
  product_id INT NOT NULL,
  status VARCHAR(20) NOT NULL,
  total NUMERIC(10,2) NOT NULL,
  created_at TIMESTAMP NOT NULL
);
CREATE INDEX orders_customer_id ON orders (customer_id);

INSERT INTO customers (name, email, city, created_at)
SELECT 'Customer ' || g, 'user' || g || '@example.com',
       (ARRAY['Milano','Roma','Torino','Napoli','Bari'])[1 + g % 5], NOW() - g * INTERVAL '1 minute'
FROM generate_series(1, 20000) g;

INSERT INTO products (name, description, price)
SELECT 'Product ' || g, REPEAT(MD5(g::text) || ' ', 20), 5 + g % 300
FROM generate_series(1, 5000) g;

INSERT INTO orders (customer_id, product_id, status, total, created_at)
SELECT 1 + g % 20000, 1 + g % 5000, (ARRAY['paid','pending','shipped','refunded'])[1 + g % 4],
       10 + g % 500, NOW() - g * INTERVAL '1 minute'
FROM generate_series(1, 300000) g;

ANALYZE;
