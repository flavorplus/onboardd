import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";

import { MockDevice, type MockBrand, type MockModePolicy, rejectedMockPassword } from "./mock-device.ts";

const mockLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect width="48" height="48" rx="12" fill="#2b6f69"/><path d="M13 25c7-8 15-11 22-8-2 9-8 16-19 18 4-3 7-6 9-10-4 3-8 5-12 5z" fill="#fff"/></svg>`;
const mockAdminPassword = "onboardd-admin";
const mockSessionToken = "local-preview-session";

export function mockSetupAPI(policy: MockModePolicy, brand: MockBrand = "default"): Plugin {
  const device = new MockDevice(policy, brand);
  return {
    name: "onboardd-local-device",
    apply: "serve",
    configureServer(server) {
      server.config.logger.info(
        `Local onboardd simulation: ${policy}; admin password “${mockAdminPassword}”; use “${rejectedMockPassword}” to reject a Wi-Fi attempt.`,
      );
      server.middlewares.use(async (request, response, next) => {
        const url = new URL(request.url ?? "/", "http://127.0.0.1");
        if (request.method === "GET" && url.pathname === "/appearance.json") {
          sendJSON(response, 200, device.appearance());
          return;
        }
        if (
          brand === "custom" &&
          request.method === "GET" &&
          url.pathname === "/appearance/logo"
        ) {
          response.statusCode = 200;
          response.setHeader("Content-Type", "image/svg+xml");
          response.setHeader("Cache-Control", "no-store");
          response.end(mockLogo);
          return;
        }
        if (!url.pathname.startsWith("/api/")) {
          next();
          return;
        }
        if (request.method === "POST" && url.pathname === "/api/v1/session") {
          try {
            const body = await readJSON(request);
            if (!isPassword(body, mockAdminPassword)) {
              sendJSON(response, 401, {
                error: {
                  code: "authentication_failed",
                  message: "The administrator password is incorrect.",
                },
              });
              return;
            }
            response.setHeader(
              "Set-Cookie",
              `onboardd_session=${mockSessionToken}; Path=/api/v1/; HttpOnly; SameSite=Strict`,
            );
            sendJSON(response, 200, { authenticated: true });
          } catch {
            sendJSON(response, 400, {
              error: { code: "invalid_json", message: "The request could not be understood." },
            });
          }
          return;
        }
        if (!hasSession(request)) {
          sendJSON(response, 401, {
            error: {
              code: "authentication_required",
              message: "Enter the administrator password to continue.",
            },
          });
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

function isPassword(body: unknown, password: string): boolean {
  return typeof body === "object" && body !== null &&
    "password" in body && body.password === password;
}

function hasSession(request: IncomingMessage): boolean {
  return (request.headers.cookie ?? "")
    .split(";")
    .some((cookie) => cookie.trim() === `onboardd_session=${mockSessionToken}`);
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
