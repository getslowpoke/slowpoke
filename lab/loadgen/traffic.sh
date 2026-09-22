#!/bin/sh
# Steady, light traffic: about 3 requests per second across the four apps.
hit() { wget -q -T 10 -O /dev/null "$1" 2>/dev/null; }
# Through the web server on the other machine, which is the whole point of that second site.
express() { wget -q -T 10 -O /dev/null --header="Host: express.lab" "http://nginx:81$1" 2>/dev/null; }
rnd() { awk -v max="$1" 'BEGIN { srand(); print int(rand() * max) + 1 }'; }

while true; do
  id=$(rnd 20000)
  hit "http://nginx/orders/$(rnd 300000)" &
  hit "http://nginx/orders"
  hit "http://nginx/customers/lookup?email=user$id@example.com" &
  # A third of the traffic asks for one's own orders, as in a dashboard that polls: the URL says
  # "me" where the route says {customer}, and a web server cannot know that "me" is a parameter.
  # It has to weigh this much, or one unpairable call in six stays inside the 20% budget and the
  # lab would not notice the day those requests start being counted twice again.
  hit "http://nginx/customers/me/orders"
  hit "http://nginx/customers/me/orders" &
  hit "http://nginx/customers/me/orders"
  hit "http://nginx/customers/$id/orders" &
  hit "http://nginx/products/search?q=ab$(rnd 9)"
  hit "http://fastapi:8000/orders/$(rnd 300000)" &
  hit "http://fastapi:8000/orders"
  hit "http://fastapi:8000/products/search?q=c$(rnd 9)" &
  express "/orders?page=$(rnd 5000)"
  express "/customers/$id/orders" &
  hit "http://go:8080/orders/$(rnd 300000)"
  hit "http://go:8080/orders/recent" &
  hit "http://go:8080/customers/$id/spend"

  # Heavier endpoints less often, like a real app.
  if [ "$(rnd 5)" = 1 ]; then
    hit http://nginx/reports/revenue &
    hit http://nginx/checkout &
    hit "http://fastapi:8000/customers/by-city?city=Roma" &
    express /dashboard &
    hit http://go:8080/stats
  fi
  wait
  sleep 1
done
