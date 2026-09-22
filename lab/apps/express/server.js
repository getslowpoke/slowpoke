const express = require('express');
const mysql = require('mysql2/promise');
const slowpoke = require('@slowpokedev/node');

slowpoke.mysql2.instrument();

const db = mysql.createPool({ uri: process.env.DATABASE_URL, connectionLimit: 5 });
const app = express();
app.use(slowpoke.express());

app.get('/', (req, res) => res.json({ app: 'express-shop' }));

app.get('/orders/:id', async (req, res) => {
  const [rows] = await db.query('SELECT id, status, total FROM orders WHERE id = ?', [req.params.id]);
  res.json(rows[0] || {});
});

// Deep OFFSET pagination: the database reads and throws away every row before the page.
app.get('/orders', async (req, res) => {
  const page = Number(req.query.page || 1);
  const [rows] = await db.query('SELECT id, status, total FROM orders ORDER BY created_at DESC LIMIT 20 OFFSET ?', [(page - 1) * 20]);
  res.json(rows);
});

// N+1 on products.
app.get('/customers/:id/orders', async (req, res) => {
  const [orders] = await db.query('SELECT id, product_id, total FROM orders WHERE customer_id = ?', [req.params.id]);
  for (const o of orders) {
    const [p] = await db.query('SELECT name FROM products WHERE id = ?', [o.product_id]);
    o.product = p[0] && p[0].name;
  }
  res.json(orders);
});

// OR across columns and a COUNT on the big table.
app.get('/dashboard', async (req, res) => {
  const [[{ n }]] = await db.query('SELECT COUNT(*) AS n FROM orders WHERE status = ? OR total > ?', ['refunded', 490]);
  res.json({ n });
});

// What cron would run: nobody waits for it, so nobody notices when it gets slower.
setInterval(() => {
  slowpoke.command('refresh-dashboard', async () => {
    await db.query('SELECT COUNT(*) AS n FROM orders WHERE status = ?', ['refunded']);
  }).catch(() => {});
}, 60000);

app.listen(3000, () => console.log('express-shop on 3000'));
