import assert from "node:assert/strict";
import test from "node:test";

import { SetupAPI } from "./api.ts";

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
        branding: {},
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
        branding: {},
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
