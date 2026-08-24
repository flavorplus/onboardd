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
