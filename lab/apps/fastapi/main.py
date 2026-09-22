import os

from fastapi import FastAPI
from sqlalchemy import create_engine, text

engine = create_engine(os.environ["DATABASE_URL"], pool_size=5)
app = FastAPI()


@app.get("/")
def home():
    return {"app": "fastapi-shop"}


@app.get("/orders/{order_id}")
def order(order_id: int):
    with engine.connect() as db:
        row = db.execute(text("SELECT id, status, total FROM orders WHERE id = :id"), {"id": order_id}).mappings().first()
        return dict(row) if row else {}


@app.get("/orders")
def orders():
    # N+1: one query per order for its customer.
    with engine.connect() as db:
        rows = db.execute(text(
            "SELECT id, customer_id, total FROM orders WHERE status = 'paid' ORDER BY created_at DESC LIMIT 25"
        )).mappings().all()
        out = []
        for r in rows:
            c = db.execute(text("SELECT name FROM customers WHERE id = :id"), {"id": r["customer_id"]}).scalar()
            out.append({"id": r["id"], "customer": c, "total": float(r["total"])})
        return out


@app.get("/products/search")
def search(q: str = ""):
    with engine.connect() as db:
        rows = db.execute(text("SELECT id, name FROM products WHERE description ILIKE :q LIMIT 10"), {"q": f"%{q}%"})
        return [dict(r) for r in rows.mappings()]


@app.get("/customers/by-city")
def by_city(city: str = "Roma"):
    # Function on the column: the planner cannot use an index even if one existed.
    with engine.connect() as db:
        rows = db.execute(text("SELECT id, name FROM customers WHERE LOWER(city) = LOWER(:c) ORDER BY created_at DESC LIMIT 20"), {"c": city})
        return [dict(r) for r in rows.mappings()]
