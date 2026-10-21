#!/usr/bin/env python3
"""Public HTTP demo. Prints business data; never prints JWTs or passwords."""
import json
import os
import uuid
import urllib.request

BASE = os.getenv("BASE_URL", "http://localhost:8080")
suffix = uuid.uuid4().hex[:10]

def call(method, path, data=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=15) as response:
        body = response.read()
        result = json.loads(body) if body else None
        print(method, path, response.status, "request_id=" + response.headers.get("X-Request-Id", ""))
        if not path.startswith("/auth/"):
            print(json.dumps(result, ensure_ascii=False, indent=2))
        return result

seller = call("POST", "/auth/register", {"email": f"seller-{suffix}@example.test", "password": "demo-password-2026", "role": "SELLER"})
buyer = call("POST", "/auth/register", {"email": f"buyer-{suffix}@example.test", "password": "demo-password-2026", "role": "USER"})
product = call("POST", "/products", {"name": "Клавиатура", "price": 123.45, "stock": 10, "category": "electronics", "status": "ACTIVE"}, seller["access_token"])
order = call("POST", "/orders", {"items": [{"product_id": product["id"], "quantity": 2}]}, buyer["access_token"])
call("GET", "/products/" + product["id"], token=buyer["access_token"])
call("POST", "/orders/" + order["id"] + "/cancel", token=buyer["access_token"])
call("GET", "/products/" + product["id"], token=buyer["access_token"])
print("Покажите записи БД: make db-show")
