-- Shop schema with the indexes a busy team forgets: orders.status, orders.created_at,
-- customers.email and products.name have none.
CREATE TABLE customers (
  id INT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  email VARCHAR(150) NOT NULL,
  city VARCHAR(80) NOT NULL,
  created_at DATETIME NOT NULL
);

CREATE TABLE products (
  id INT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(150) NOT NULL,
  description TEXT NOT NULL,
  price DECIMAL(10,2) NOT NULL
);

CREATE TABLE orders (
  id INT AUTO_INCREMENT PRIMARY KEY,
  customer_id INT NOT NULL,
  product_id INT NOT NULL,
  status VARCHAR(20) NOT NULL,
  total DECIMAL(10,2) NOT NULL,
  created_at DATETIME NOT NULL,
  KEY orders_customer_id (customer_id)
);

INSERT INTO customers (name, email, city, created_at)
SELECT CONCAT('Customer ', seq), CONCAT('user', seq, '@example.com'),
       ELT(1 + seq % 5, 'Milano', 'Roma', 'Torino', 'Napoli', 'Bari'), NOW() - INTERVAL seq MINUTE
FROM seq_1_to_20000;

INSERT INTO products (name, description, price)
SELECT CONCAT('Product ', seq), REPEAT(CONCAT(MD5(seq), ' '), 20), 5 + seq % 300
FROM seq_1_to_5000;

INSERT INTO orders (customer_id, product_id, status, total, created_at)
SELECT 1 + seq % 20000, 1 + seq % 5000, ELT(1 + seq % 4, 'paid', 'pending', 'shipped', 'refunded'),
       10 + seq % 500, NOW() - INTERVAL seq MINUTE
FROM seq_1_to_300000;
