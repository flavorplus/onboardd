import type {
  Bootstrap,
  Branding,
  Capabilities,
  Mode,
  KnownNetwork,
  Network,
  Operation,
  OperationKind,
  OperationState,
} from "../src/model.ts";

export const mockCSRFToken = "onboardd-local-preview-token";
export const rejectedMockPassword = "wrong-password";

export type MockModePolicy = "both" | "network" | "standalone";

export type MockBrand = "default" | "custom";

const defaultBranding: Branding = {
  product_name: "Device setup",
  device_name: "Device",
  title: "How should this device connect?",
  subtitle: "Choose Wi-Fi for normal network access, or keep this device available as its own network.",
  primary_color: "#cd2455",
  background_color: "#f8eff3",
};

const customBranding: Branding = {
  product_name: "InkyPi",
  device_name: "Kitchen Display",
  title: "Set up your Kitchen Display",
  subtitle: "Connect your display to Wi-Fi or keep it available as a private offline network.",
  primary_color: "#2b6f69",
  background_color: "#eef7f3",
  logo_url: "/api/v1/branding/logo",
};

export interface MockRequest {
  method: string;
  path: string;
  csrfToken?: string;
  body?: unknown;
}

export interface MockResponse {
  status: number;
  body: unknown;
}

interface InternalOperation extends Operation {
  startedAt: number;
  outcome: "succeeded" | "failed";
  interruptionPending: boolean;
}

const networks: Network[] = [
  { ssid: "Studio Wi-Fi", security: "protected", strength: 91 },
  { ssid: "Guest Wi-Fi", security: "open", strength: 73 },
  { ssid: "Workshop", security: "protected", strength: 48 },
  { ssid: "Weak test network", security: "protected", strength: 19 },
];

export class MockDevice {
  readonly capabilities: Capabilities;

  private currentMode: Mode = "setup";
  private operation?: InternalOperation;
  private sequence = 0;
  private readonly branding: Branding;
  private knownNetworks: KnownNetwork[] = [
    {
      uuid: "329cdb0f-d696-4f63-a17e-84ac66582f43",
      ssid: "Studio Wi-Fi",
      managed_by_onboardd: true,
      active: false,
      automatic: true,
      can_connect: true,
      can_forget: true,
    },
    {
      uuid: "b01a1c10-ce1e-40e7-9fe2-7ebcf30a43c7",
      ssid: "Venue system Wi-Fi",
      managed_by_onboardd: false,
      active: false,
      automatic: true,
      can_connect: false,
      can_forget: false,
    },
  ];

  constructor(policy: MockModePolicy = "both", brand: MockBrand = "default") {
    this.capabilities = {
      network: policy === "both" || policy === "network",
      standalone: policy === "both" || policy === "standalone",
    };
    this.branding = brand === "custom" ? customBranding : defaultBranding;
  }

  handle(request: MockRequest, now = Date.now()): MockResponse {
    if (request.method === "GET" && request.path === "/api/v1/setup") {
      return this.json(200, this.bootstrap(now));
    }
    if (request.method === "GET" && request.path === "/api/v1/networks") {
      if (!this.capabilities.network) return this.modeUnavailable("Wi-Fi network");
      return this.json(200, { networks });
    }
    if (request.method === "GET" && request.path === "/api/v1/known-networks") {
      if (!this.capabilities.network) return this.modeUnavailable("Wi-Fi network");
      return this.json(200, { networks: this.knownNetworks });
    }
    const knownNetworkPrefix = "/api/v1/known-networks/";
    if (request.method === "DELETE" && request.path.startsWith(knownNetworkPrefix)) {
      if (!this.mutationAllowed(request)) return this.requestNotAllowed();
      if (!this.capabilities.network) return this.modeUnavailable("Wi-Fi network");
      return this.forgetKnownNetwork(
        decodeURIComponent(request.path.slice(knownNetworkPrefix.length)),
      );
    }
    const knownNetworkConnectSuffix = "/connect";
    if (
      request.method === "POST" &&
      request.path.startsWith(knownNetworkPrefix) &&
      request.path.endsWith(knownNetworkConnectSuffix)
    ) {
      if (!this.mutationAllowed(request)) return this.requestNotAllowed();
      if (!this.capabilities.network) return this.modeUnavailable("Wi-Fi network");
      return this.startKnownNetwork(
        decodeURIComponent(
          request.path.slice(
            knownNetworkPrefix.length,
            -knownNetworkConnectSuffix.length,
          ),
        ),
        now,
      );
    }
    if (request.method === "POST" && request.path === "/api/v1/connections") {
      if (!this.mutationAllowed(request)) return this.requestNotAllowed();
      if (!this.capabilities.network) return this.modeUnavailable("Wi-Fi network");
      return this.startConnection(request.body, now);
    }
    if (request.method === "POST" && request.path === "/api/v1/standalone") {
      if (!this.mutationAllowed(request)) return this.requestNotAllowed();
      if (!this.capabilities.standalone) return this.modeUnavailable("Standalone");
      return this.startStandalone(request.body, now);
    }
    const operationPrefix = "/api/v1/operations/";
    if (request.method === "GET" && request.path.startsWith(operationPrefix)) {
      const id = decodeURIComponent(request.path.slice(operationPrefix.length));
      return this.getOperation(id, now);
    }
    return this.error(404, "api_not_found", "This setup API route does not exist.");
  }

  private bootstrap(now: number): Bootstrap {
    const operation = this.operationSnapshot(now);
    const applicationReady = !this.operation ||
      (operation?.state === "succeeded" && now - this.operation.startedAt >= 5000);
    return {
      branding: this.branding,
      capabilities: this.capabilities,
      current_mode: this.currentMode,
      operation,
      handoff: {
        setup_url: "http://127.0.0.1:5173/",
        application: {
          label: `Open ${this.branding.product_name}`,
          url: applicationReady ? "http://device.local/" : undefined,
          ready: applicationReady,
        },
        standalone: this.capabilities.standalone
          ? { ssid: "Device-AB12CD34", password: "standalone-password" }
          : undefined,
      },
      csrf_token: mockCSRFToken,
    };
  }

  private startConnection(body: unknown, now: number): MockResponse {
    if (this.operationActive(now)) return this.conflict(now);
    const input = connectionInput(body);
    if (!input) {
      return this.error(400, "invalid_connection", "Enter valid network details.");
    }
    const outcome = input.password === rejectedMockPassword ? "failed" : "succeeded";
    return this.accept("connect", input.ssid, outcome, now);
  }

  private forgetKnownNetwork(uuid: string): MockResponse {
    const index = this.knownNetworks.findIndex((network) => network.uuid === uuid);
    if (index < 0) {
      return this.error(404, "known_network_not_found", "This saved network no longer exists.");
    }
    const network = this.knownNetworks[index]!;
    if (!network.managed_by_onboardd) {
      return this.error(
        403,
        "network_read_only",
        "This network profile is managed outside onboardd and cannot be forgotten here.",
      );
    }
    if (network.active) {
      return this.error(
        409,
        "active_network",
        "The network currently in use cannot be forgotten. Connect to another network first.",
      );
    }
    this.knownNetworks.splice(index, 1);
    return this.json(200, { forgotten: uuid });
  }

  private startKnownNetwork(uuid: string, now: number): MockResponse {
    if (this.operationActive(now)) return this.conflict(now);
    const network = this.knownNetworks.find((candidate) => candidate.uuid === uuid);
    if (!network) {
      return this.error(404, "known_network_not_found", "This saved network no longer exists.");
    }
    if (network.active) {
      return this.error(409, "active_network", "The device is already connected to this network.");
    }
    if (!network.can_connect) {
      return this.error(
        403,
        "network_read_only",
        "This network profile is managed outside onboardd and cannot be activated here.",
      );
    }
    return this.accept("connect", network.ssid, "succeeded", now);
  }

  private startStandalone(body: unknown, now: number): MockResponse {
    if (this.operationActive(now)) return this.conflict(now);
    if (!isRecord(body) || body.confirm !== true) {
      return this.error(400, "confirmation_required", "Confirm standalone mode before continuing.");
    }
    return this.accept("standalone", undefined, "succeeded", now);
  }

  private accept(
    kind: OperationKind,
    network: string | undefined,
    outcome: "succeeded" | "failed",
    now: number,
  ): MockResponse {
    this.sequence += 1;
    this.operation = {
      id: `local-operation-${this.sequence}`,
      kind,
      state: "pending",
      network,
      startedAt: now,
      outcome,
      interruptionPending: true,
    };
    return this.json(202, { operation: publicOperation(this.operation) });
  }

  private getOperation(id: string, now: number): MockResponse {
    if (!this.operation || this.operation.id !== id) {
      return this.error(404, "operation_not_found", "This setup operation is no longer available.");
    }
    const elapsed = now - this.operation.startedAt;
    if (this.operation.interruptionPending && elapsed >= 700 && elapsed < 2400) {
      this.operation.interruptionPending = false;
      return this.error(503, "simulated_disconnect", "The simulated device is changing networks.");
    }
    return this.json(200, { operation: this.operationSnapshot(now) });
  }

  private operationSnapshot(now: number): Operation | undefined {
    if (!this.operation) return undefined;
    const elapsed = now - this.operation.startedAt;
    let state: OperationState = "pending";
    if (elapsed >= 500 && elapsed < 2600) state = "running";
    if (elapsed >= 2600) state = this.operation.outcome;
    this.operation.state = state;
    if (state === "succeeded") {
      this.currentMode = this.operation.kind === "standalone" ? "standalone" : "network";
      this.operation.failure = undefined;
    } else if (state === "failed") {
      this.operation.failure = {
        code: "connection_failed",
        message: "The device could not join that network. Check the network name and password, then try again.",
      };
    }
    return publicOperation(this.operation);
  }

  private operationActive(now: number): boolean {
    const operation = this.operationSnapshot(now);
    return operation?.state === "pending" || operation?.state === "running";
  }

  private conflict(now: number): MockResponse {
    return this.json(409, {
      error: {
        code: "operation_in_progress",
        message: "Another network change is already in progress.",
      },
      operation: this.operationSnapshot(now),
    });
  }

  private mutationAllowed(request: MockRequest): boolean {
    return request.csrfToken === mockCSRFToken;
  }

  private requestNotAllowed(): MockResponse {
    return this.error(403, "request_not_allowed", "Refresh the setup page and try again.");
  }

  private modeUnavailable(label: string): MockResponse {
    return this.error(403, "mode_unavailable", `${label} mode is not available.`);
  }

  private error(status: number, code: string, message: string): MockResponse {
    return this.json(status, { error: { code, message } });
  }

  private json(status: number, body: unknown): MockResponse {
    return { status, body };
  }
}

function publicOperation(operation: InternalOperation): Operation {
  return {
    id: operation.id,
    kind: operation.kind,
    state: operation.state,
    network: operation.network,
    failure: operation.failure,
  };
}

function connectionInput(body: unknown): { ssid: string; password: string } | undefined {
  if (!isRecord(body) || typeof body.ssid !== "string" || typeof body.password !== "string") {
    return undefined;
  }
  if (body.ssid.length === 0 || body.ssid.length > 32) return undefined;
  return { ssid: body.ssid, password: body.password };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
