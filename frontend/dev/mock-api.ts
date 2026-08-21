import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";

import { MockDevice, type MockModePolicy, rejectedMockPassword } from "./mock-device.ts";

export function mockSetupAPI(policy: MockModePolicy): Plugin {
  const device = new MockDevice(policy);
  return {
    name: "onboardd-local-device",
    apply: "serve",
    configureServer(server) {
      server.config.logger.info(
        `Local onboardd simulation: ${policy}; use “${rejectedMockPassword}” to reject a Wi-Fi attempt.`,
      );
      server.middlewares.use(async (request, response, next) => {
        const url = new URL(request.url ?? "/", "http://127.0.0.1");
        if (!url.pathname.startsWith("/api/")) {
          next();
          return;
        }
        try {
          const body = request.method === "POST" ? await readJSON(request) : undefined;
          const result = device.handle({
            method: request.method ?? "GET",
            path: url.pathname,
            csrfToken: header(request, "x-onboardd-csrf"),
            body,
          });
          sendJSON(response, result.status, result.body);
        } catch {
          sendJSON(response, 400, {
            error: { code: "invalid_json", message: "The request could not be understood." },
          });
        }
      });
    },
  };
}

async function readJSON(request: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of request) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    size += buffer.length;
    if (size > 4096) throw new Error("body too large");
    chunks.push(buffer);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

function header(request: IncomingMessage, name: string): string | undefined {
  const value = request.headers[name];
  return Array.isArray(value) ? value[0] : value;
}

function sendJSON(response: ServerResponse, status: number, body: unknown): void {
  response.statusCode = status;
  response.setHeader("Content-Type", "application/json; charset=utf-8");
  response.setHeader("Cache-Control", "no-store");
  response.end(JSON.stringify(body));
}
