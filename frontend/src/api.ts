import type { Bootstrap, Network, Operation } from "./model.ts";

interface ErrorBody {
  error?: {
    code?: string;
    message?: string;
  };
  operation?: Operation;
}

export class APIError extends Error {
  readonly code: string;
  readonly operation?: Operation;

  constructor(code: string, message: string, operation?: Operation) {
    super(message);
    this.name = "APIError";
    this.code = code;
    this.operation = operation;
  }
}

export class SetupAPI {
  private csrfToken = "";

  async bootstrap(): Promise<Bootstrap> {
    const result = await this.request<Bootstrap>("/api/v1/setup");
    this.csrfToken = result.csrf_token;
    return result;
  }

  async networks(): Promise<Network[]> {
    const result = await this.request<{ networks: Network[] }>("/api/v1/networks");
    return result.networks;
  }

  async connect(input: {
    ssid: string;
    password: string;
    open: boolean;
    hidden: boolean;
  }): Promise<Operation> {
    const result = await this.request<{ operation: Operation }>("/api/v1/connections", {
      method: "POST",
      body: JSON.stringify(input),
    });
    return result.operation;
  }

  async standalone(): Promise<Operation> {
    const result = await this.request<{ operation: Operation }>("/api/v1/standalone", {
      method: "POST",
      body: JSON.stringify({ confirm: true }),
    });
    return result.operation;
  }

  async operation(id: string): Promise<Operation> {
    const result = await this.request<{ operation: Operation }>(
      `/api/v1/operations/${encodeURIComponent(id)}`,
    );
    return result.operation;
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) {
      headers.set("Content-Type", "application/json");
      headers.set("X-Onboardd-CSRF", this.csrfToken);
    }
    const response = await fetch(path, {
      ...init,
      headers,
      cache: "no-store",
      credentials: "same-origin",
    });
    const body = (await response.json()) as T & ErrorBody;
    if (!response.ok) {
      throw new APIError(
        body.error?.code ?? "request_failed",
        body.error?.message ?? "The request could not be completed.",
        body.operation,
      );
    }
    return body;
  }
}
