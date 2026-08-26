import assert from "node:assert/strict";
import test from "node:test";

import { SetupAPI } from "./api.ts";

test("appearance loads before authentication", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    assert.equal(String(input), "/appearance.json");
    return Response.json({
      product_name: "InkyPi",
      device_name: "Kitchen Display",
      title: "Set up Kitchen Display",
      subtitle: "Choose a connection.",
      primary_color: "#123456",
      background_color: "#f1f2f3",
      logo_url: "/appearance/logo",
    });
  }) as typeof fetch;

  try {
    const appearance = await new SetupAPI().appearance();
    assert.equal(appearance.product_name, "InkyPi");
    assert.equal(appearance.logo_url, "/appearance/logo");
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("login sends the administrator password without a CSRF header", async () => {
  const originalFetch = globalThis.fetch;
  let loginObserved = false;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    assert.equal(String(input), "/api/v1/session");
    assert.equal(init?.method, "POST");
    assert.equal(new Headers(init?.headers).get("X-Onboardd-CSRF"), null);
    assert.deepEqual(JSON.parse(String(init?.body)), { password: "admin-password" });
    assert.equal(init?.credentials, "same-origin");
    loginObserved = true;
    return Response.json({ authenticated: true });
  }) as typeof fetch;

  try {
    await new SetupAPI().login("admin-password");
    assert.equal(loginObserved, true);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("a stalled request is aborted so operation polling can retry", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = ((_input: RequestInfo | URL, init?: RequestInit) =>
    new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener(
        "abort",
        () => reject(init.signal?.reason),
        { once: true },
      );
    })) as typeof fetch;

  try {
    await assert.rejects(
      new SetupAPI(5).bootstrap(),
      (error: unknown) => error instanceof DOMException && error.name === "AbortError",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("DELETE known network sends the bootstrap CSRF token", async () => {
  const originalFetch = globalThis.fetch;
  const token = "local-csrf-token";
  let deleteObserved = false;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/setup") {
      return Response.json({
        capabilities: { network: true, standalone: true },
        current_mode: "setup",
        csrf_token: token,
      });
    }
    if (path.startsWith("/api/v1/known-networks/")) {
      deleteObserved = true;
      assert.equal(init?.method, "DELETE");
      assert.equal(new Headers(init?.headers).get("X-Onboardd-CSRF"), token);
      return Response.json({ forgotten: path.split("/").at(-1) });
    }
    return Response.json({}, { status: 404 });
  }) as typeof fetch;

  try {
    const api = new SetupAPI();
    await api.bootstrap();
    await api.forgetKnownNetwork("329cdb0f-d696-4f63-a17e-84ac66582f43");
    assert.equal(deleteObserved, true);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("POST known network activation sends the bootstrap CSRF token", async () => {
  const originalFetch = globalThis.fetch;
  const token = "local-csrf-token";
  const uuid = "0a3aeac5-3e46-4f46-b9b0-99b2f83d4cb1";
  let activationObserved = false;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/api/v1/setup") {
      return Response.json({
        capabilities: { network: true, standalone: true },
        current_mode: "setup",
        csrf_token: token,
      });
    }
    if (path === `/api/v1/known-networks/${uuid}/connect`) {
      activationObserved = true;
      assert.equal(init?.method, "POST");
      assert.equal(new Headers(init?.headers).get("X-Onboardd-CSRF"), token);
      return Response.json({
        operation: { id: "saved", kind: "connect", state: "pending", network: "Workshop" },
      });
    }
    return Response.json({}, { status: 404 });
  }) as typeof fetch;

  try {
    const api = new SetupAPI();
    await api.bootstrap();
    const operation = await api.connectKnownNetwork(uuid);
    assert.equal(operation.network, "Workshop");
    assert.equal(activationObserved, true);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
