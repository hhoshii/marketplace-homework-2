#!/usr/bin/env python3
"""Verify Commerce remains usable with User Service temporarily stopped."""
import json
import os
import subprocess
import time
import urllib.error
import urllib.request
import uuid

BASE = os.getenv("BASE_URL", "http://localhost:8080")
suffix = uuid.uuid4().hex[:10]

def call(method, path, data=None, token=None, expected=200):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=15)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        result = json.loads(raw) if raw else None
        assert response.status == expected, (method, path, response.status)
        uuid.UUID(response.headers["X-Request-Id"])
        return result

seller = call("POST", "/auth/register", {"email": f"isolation-seller-{suffix}@example.test", "password": "isolation-password", "role": "SELLER"}, expected=201)
buyer = call("POST", "/auth/register", {"email": f"isolation-user-{suffix}@example.test", "password": "isolation-password", "role": "USER"}, expected=201)
product = call("POST", "/products", {"name": "Isolation", "price": 3, "stock": 2, "category": "test", "status": "ACTIVE"}, seller["access_token"], 201)
try:
    subprocess.run(["docker", "compose", "stop", "user-service"], check=True)
    call("GET", "/products/" + product["id"], token=buyer["access_token"])
    order = call("POST", "/orders", {"items": [{"product_id": product["id"], "quantity": 1}]}, buyer["access_token"], 201)
    call("POST", "/orders/" + order["id"] + "/cancel", token=buyer["access_token"])
    restored = call("GET", "/products/" + product["id"], token=buyer["access_token"])
    assert restored["stock"] == 2
    failure = call("POST", "/auth/login", {"email": "unavailable@example.test", "password": "isolation-password"}, expected=503)
    assert failure["error_code"] == "DEPENDENCY_UNAVAILABLE"
    print("PASS: Commerce reads/orders/cancel work without User Service; auth returns contract 503.")
finally:
    subprocess.run(["docker", "compose", "up", "-d", "--no-deps", "user-service"], check=True)
    for _ in range(30):
        try:
            call("POST", "/auth/refresh", {"refresh_token": "invalid"}, expected=401)
            print("User Service restored.")
            break
        except (AssertionError, urllib.error.URLError):
            time.sleep(1)
    else:
        raise RuntimeError("User Service did not recover")
