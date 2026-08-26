import * as QRCode from "qrcode";

import { APIError, SetupAPI } from "./api.ts";
import {
  brandingPalette,
  initialView,
  modeLabel,
  needsBrowserHandoff,
  strengthLabel,
  wifiQRPayload,
  type Bootstrap,
  type Branding,
  type KnownNetwork,
  type Network,
  type Operation,
  type StandaloneHandoff,
} from "./model.ts";

const appElement = document.querySelector<HTMLElement>("#app");
if (!appElement) throw new Error("setup root is missing");
const app: HTMLElement = appElement;

const api = new SetupAPI();
let bootstrap: Bootstrap;
let branding: Branding = {
  product_name: "Device",
  device_name: "Device",
  title: "How should this device connect?",
  subtitle: "Choose Wi-Fi or standalone mode.",
  primary_color: "#cd2455",
  background_color: "#f8eff3",
};
let viewRevision = 0;

void start();

async function start(): Promise<void> {
  renderLoading("Opening device setup…");
  try {
    branding = await api.appearance();
    applyBranding();
    bootstrap = await api.bootstrap();
    const view = initialView(bootstrap);
    if (view === "operation" && bootstrap.operation) {
      await monitorOperation(bootstrap.operation);
      return;
    }
    if (view === "networks") {
      await showNetworks();
      return;
    }
    if (view === "standalone") {
      showStandaloneConfirmation();
      return;
    }
    showModeChoice();
  } catch (error) {
    if (showLoginIfRequired(error)) return;
    showLoadFailure(error);
  }
}

function showLogin(): void {
  viewRevision += 1;
  const shell = element("section", "setup-shell");
  const header = element("header", "setup-header");
  const wordmark = element("div", "wordmark");
  wordmark.append(brandMark(), textElement("span", branding.product_name));
  header.append(wordmark);

  const content = element("div", "setup-content");
  content.append(
    textElement("p", "Administrator access", "eyebrow"),
    textElement("h1", `Unlock ${branding.device_name} setup`),
    textElement(
      "p",
      "Enter the administrator password to view or change this device’s network settings.",
      "lede",
    ),
  );
  const form = element("form", "form-stack") as HTMLFormElement;
  const password = inputField("Administrator password", "password", "password", "", "");
  password.input.autocomplete = "current-password";
  password.input.maxLength = 256;
  const errorSlot = element("div", "form-error");
  errorSlot.setAttribute("role", "alert");
  form.append(password.wrapper, errorSlot, submitButton("Unlock setup"));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    errorSlot.replaceChildren();
    const submit = form.querySelector<HTMLButtonElement>("button[type=submit]");
    if (submit) submit.disabled = true;
    try {
      await api.login(password.input.value);
      password.input.value = "";
      await start();
    } catch (error) {
      password.input.value = "";
      if (submit) submit.disabled = false;
      errorSlot.append(textElement("p", messageFrom(error)));
      password.input.focus();
    }
  });
  content.append(form, helpText("The password is stored only on this device."));
  shell.append(header, content);
  app.replaceChildren(shell);
  password.input.focus();
}

function showLoginIfRequired(error: unknown): boolean {
  if (!(error instanceof APIError) || error.code !== "authentication_required") return false;
  showLogin();
  return true;
}

function frame(options: {
  eyebrow: string;
  title: string;
  description: string;
  back?: () => void;
}): HTMLElement {
  viewRevision += 1;
  const shell = element("section", "setup-shell");
  const header = element("header", "setup-header");
  if (options.back) {
    const back = button("Back", "button button-quiet", options.back);
    back.setAttribute("aria-label", "Go back");
    header.append(back);
  }
  const wordmark = element("div", "wordmark");
  wordmark.append(brandMark(), textElement("span", branding.product_name));
  header.append(wordmark);

  const content = element("div", "setup-content");
  content.append(
    textElement("p", options.eyebrow, "eyebrow"),
    textElement("h1", options.title),
    textElement("p", options.description, "lede"),
  );
  shell.append(header, content);
  app.replaceChildren(shell);
  return content;
}

function showModeChoice(): void {
  const content = frame({
    eyebrow: modeLabel(bootstrap.current_mode),
    title: branding.title,
    description: branding.subtitle,
  });
  const choices = element("div", "choice-grid");
  if (bootstrap.capabilities.network) {
    choices.append(
      choiceButton(
        "Wi-Fi network",
        "Connect this device to an existing wireless network.",
        "Recommended",
        () => void showNetworks(),
      ),
    );
  }
  if (bootstrap.capabilities.standalone) {
    choices.append(
      choiceButton(
        "Standalone",
        "Use this device directly without Internet or another network.",
        "Works offline",
        showStandaloneConfirmation,
      ),
    );
  }
  const handoff = normalBrowserHandoff();
  if (handoff) content.append(handoff);
  content.append(choices);
  if (bootstrap.capabilities.network) {
    const management = element("div", "management-actions");
    management.append(
      button("Manage known networks", "button button-quiet", () => void showKnownNetworks()),
    );
    content.append(management);
  }
  content.append(helpText("You can change this choice later."));
}

function applyBranding(): void {
  const palette = brandingPalette(branding);
  const root = document.documentElement;
  root.style.setProperty("--accent", palette.accent);
  root.style.setProperty("--accent-dark", palette.accentDark);
  root.style.setProperty("--accent-pale", palette.accentPale);
  root.style.setProperty("--accent-wash", palette.accentWash);
  root.style.setProperty("--line", palette.line);
  root.style.setProperty("--accent-rgb", palette.accentRGB);
  root.style.setProperty("--background-rgb", palette.backgroundRGB);
  root.style.setProperty("--accent-gradient", palette.gradient);
  document.title = `${branding.product_name} setup`;
}

function brandMark(): HTMLElement {
  if (branding.logo_url) {
    const logo = document.createElement("img");
    logo.className = "wordmark-logo";
    logo.src = branding.logo_url;
    logo.alt = "";
    logo.decoding = "async";
    logo.referrerPolicy = "no-referrer";
    logo.addEventListener("error", () => logo.replaceWith(wirelessMark()), { once: true });
    return logo;
  }
  return wirelessMark();
}

function wirelessMark(): HTMLElement {
  const mark = element("span", "wordmark-mark");
  mark.setAttribute("aria-hidden", "true");
  for (let level = 1; level <= 4; level += 1) {
    mark.append(element("span", `wordmark-bar wordmark-bar-${level}`));
  }
  return mark;
}

async function showNetworks(): Promise<void> {
  const content = frame({
    eyebrow: "Wi-Fi setup",
    title: "Choose a network",
    description: "Select the network this device should use.",
    back: bootstrap.capabilities.standalone ? showModeChoice : undefined,
  });
  const status = textElement("p", "Looking for nearby networks…", "inline-status");
  status.setAttribute("role", "status");
  const handoff = normalBrowserHandoff();
  if (handoff) content.append(handoff);
  content.append(status);
  try {
    const networks = await api.networks();
    status.remove();
    const list = element("div", "network-list");
    for (const network of networks) {
      list.append(networkButton(network));
    }
    if (networks.length === 0) {
      list.append(helpText("No networks were found. Move closer to your router or try again."));
    }
    const actions = element("div", "row-actions");
    actions.append(
      button("Scan again", "button button-secondary", () => void showNetworks()),
      button("Enter a hidden network", "button button-quiet", showHiddenNetwork),
      button("Known networks", "button button-quiet", () => void showKnownNetworks()),
    );
    content.append(list, actions);
  } catch (error) {
    status.remove();
    if (showLoginIfRequired(error)) return;
    showInlineError(content, messageFrom(error), () => void showNetworks());
  }
}

async function showKnownNetworks(successMessage?: string): Promise<void> {
  const content = frame({
    eyebrow: "Wi-Fi profiles",
    title: "Known networks",
    description: "Review Wi-Fi profiles saved on this device.",
    back: bootstrap.capabilities.standalone ? showModeChoice : () => void showNetworks(),
  });
  if (successMessage) {
    const success = textElement("p", successMessage, "inline-success");
    success.setAttribute("role", "status");
    content.append(success);
  }
  const status = textElement("p", "Loading saved networks…", "inline-status");
  status.setAttribute("role", "status");
  content.append(status);
  try {
    const networks = await api.knownNetworks();
    status.remove();
    const list = element("div", "known-network-list");
    for (const network of networks) {
      list.append(knownNetworkRow(network));
    }
    if (networks.length === 0) {
      list.append(helpText("No saved Wi-Fi networks apply to this device."));
    }
    content.append(
      list,
      helpText("System-managed profiles are shown for context and remain read-only."),
    );
  } catch (error) {
    status.remove();
    if (showLoginIfRequired(error)) return;
    showInlineError(content, messageFrom(error), () => void showKnownNetworks());
  }
}

function knownNetworkRow(network: KnownNetwork): HTMLElement {
  const row = element("article", "known-network-row");
  const identity = element("div", "network-identity");
  const details = [network.managed_by_onboardd ? "Saved by onboardd" : "System-managed"];
  if (network.active) details.push("Currently connected");
  else if (network.automatic) details.push("Connects automatically");
  identity.append(
    textElement("strong", network.ssid),
    textElement("span", details.join(" · "), "network-meta"),
  );
  row.append(identity);
  const actions = element("div", "known-network-actions");
  if (network.can_connect) {
    actions.append(
      button("Connect", "button button-primary button-compact", () => {
        showKnownNetworkConfirmation(network);
      }),
    );
  }
  if (network.can_forget) {
    actions.append(
      button("Forget", "button button-danger button-compact", () => {
        showForgetNetworkConfirmation(network);
      }),
    );
  }
  if (actions.childElementCount > 0) {
    row.append(actions);
  } else {
    row.append(
      textElement(
        "span",
        network.active ? "In use" : "Read only",
        "known-network-state",
      ),
    );
  }
  return row;
}

function showKnownNetworkConfirmation(network: KnownNetwork): void {
  const content = frame({
    eyebrow: "Known networks",
    title: `Connect to ${network.ssid}?`,
    description: "The device will use the password already saved with this Wi-Fi profile.",
    back: () => void showKnownNetworks(),
  });
  const note = element("div", "notice");
  note.append(
    textElement("strong", "Protected connection change"),
    textElement(
      "p",
      "If this network cannot be reached, the device will restore the current connection automatically.",
    ),
  );
  const errorSlot = element("div", "form-error");
  errorSlot.setAttribute("role", "alert");
  const connect = button("Connect", "button button-primary", async () => {
    connect.disabled = true;
    errorSlot.replaceChildren();
    try {
      await monitorOperation(await api.connectKnownNetwork(network.uuid));
    } catch (error) {
      if (showLoginIfRequired(error)) return;
      connect.disabled = false;
      errorSlot.append(textElement("p", messageFrom(error)));
    }
  });
  const handoff = normalBrowserHandoff();
  if (handoff) content.append(handoff);
  const actions = element("div", "row-actions");
  actions.append(
    connect,
    button("Cancel", "button button-secondary", () => void showKnownNetworks()),
  );
  content.append(note, errorSlot, actions);
  connect.focus();
}

function showForgetNetworkConfirmation(network: KnownNetwork): void {
  const content = frame({
    eyebrow: "Known networks",
    title: `Forget ${network.ssid}?`,
    description: "The saved Wi-Fi profile and its password will be removed from this device.",
    back: () => void showKnownNetworks(),
  });
  const note = element("div", "notice notice-warning");
  note.append(
    textElement("strong", "This cannot be undone"),
    textElement("p", "You will need to enter the Wi-Fi password again to reconnect later."),
  );
  const errorSlot = element("div", "form-error");
  errorSlot.setAttribute("role", "alert");
  const actions = element("div", "row-actions");
  const forget = button("Forget network", "button button-danger", async () => {
    forget.disabled = true;
    errorSlot.replaceChildren();
    try {
      await api.forgetKnownNetwork(network.uuid);
      await showKnownNetworks(`${network.ssid} was forgotten.`);
    } catch (error) {
      if (showLoginIfRequired(error)) return;
      forget.disabled = false;
      errorSlot.append(textElement("p", messageFrom(error)));
    }
  });
  actions.append(
    forget,
    button("Keep network", "button button-secondary", () => void showKnownNetworks()),
  );
  content.append(note, errorSlot, actions);
  forget.focus();
}

function networkButton(network: Network): HTMLButtonElement {
  const control = button("", "network-row", () => showCredentials(network, false));
  control.removeAttribute("aria-label");
  const identity = element("span", "network-identity");
  identity.append(
    textElement("strong", network.ssid),
    textElement(
      "span",
      network.security === "open" ? "Open network" : "Password required",
      "network-meta",
    ),
  );
  const signal = element("span", "signal-label");
  signal.setAttribute("data-strength", String(Math.min(4, Math.max(1, Math.ceil(network.strength / 25)))));
  const bars = element("span", "signal-bars");
  bars.setAttribute("aria-hidden", "true");
  for (let level = 1; level <= 4; level += 1) {
    bars.append(element("span", `signal-bar signal-bar-${level}`));
  }
  signal.append(bars, textElement("span", strengthLabel(network.strength), "signal-description"));
  control.append(identity, signal);
  return control;
}

function showHiddenNetwork(): void {
  const content = frame({
    eyebrow: "Hidden network",
    title: "Enter network details",
    description: "Type the exact Wi-Fi name and choose whether it uses a password.",
    back: () => void showNetworks(),
  });
  const form = element("form", "form-stack") as HTMLFormElement;
  const ssid = inputField("Network name", "text", "ssid", "", "Enter the exact name");
  const security = selectField("Security", "security", [
    ["protected", "Password protected"],
    ["open", "Open network"],
  ]);
  form.append(ssid.wrapper, security.wrapper, submitButton("Continue"));
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const name = ssid.input.value;
    if (!name) {
      ssid.input.setCustomValidity("Enter the network name.");
      ssid.input.reportValidity();
      return;
    }
    showCredentials(
      { ssid: name, security: security.select.value === "open" ? "open" : "protected", strength: 0 },
      true,
    );
  });
  content.append(form);
  ssid.input.focus();
}

function showCredentials(network: Network, hidden: boolean): void {
  const open = network.security === "open";
  const content = frame({
    eyebrow: hidden ? "Hidden network" : "Wi-Fi network",
    title: network.ssid,
    description: open
      ? "This network does not require a password."
      : "Enter the password for this network.",
    back: hidden ? showHiddenNetwork : () => void showNetworks(),
  });
  const form = element("form", "form-stack") as HTMLFormElement;
  let password: ReturnType<typeof inputField> | undefined;
  if (!open) {
    password = inputField("Wi-Fi password", "password", "password", "", "At least 8 characters");
    password.input.autocomplete = "current-password";
    password.input.minLength = 8;
    password.input.maxLength = 64;
    password.input.required = true;
    form.append(password.wrapper);
  }
  const errorSlot = element("div", "form-error");
  errorSlot.setAttribute("role", "alert");
  form.append(errorSlot, submitButton("Connect"));
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    errorSlot.replaceChildren();
    const submit = form.querySelector<HTMLButtonElement>("button[type=submit]");
    if (submit) submit.disabled = true;
    try {
      const operation = await api.connect({
        ssid: network.ssid,
        password: password?.input.value ?? "",
        open,
        hidden,
      });
      if (password) password.input.value = "";
      await monitorOperation(operation);
    } catch (error) {
      if (showLoginIfRequired(error)) return;
      if (submit) submit.disabled = false;
      errorSlot.append(textElement("p", messageFrom(error)));
    }
  });
  content.append(form);
  (password?.input ?? form.querySelector<HTMLButtonElement>("button"))?.focus();
}

function showStandaloneConfirmation(): void {
  const content = frame({
    eyebrow: "Standalone mode",
    title: "Use this device without another network?",
    description: "The device will keep its own Wi-Fi network active. Internet access is not required.",
    back: bootstrap.capabilities.network ? showModeChoice : undefined,
  });
  const note = element("div", "notice");
  note.append(
    textElement("strong", "What happens next"),
    textElement("p", "Your current connection may pause briefly. Rejoin this device’s Wi-Fi if asked."),
  );
  const confirm = button("Use standalone mode", "button button-primary standalone-confirm", async () => {
    confirm.disabled = true;
    try {
      await monitorOperation(await api.standalone());
    } catch (error) {
      if (showLoginIfRequired(error)) return;
      confirm.disabled = false;
      showInlineError(content, messageFrom(error), showStandaloneConfirmation);
    }
  });
  const handoff = normalBrowserHandoff();
  if (handoff) content.append(handoff);
  const standaloneDetails = bootstrap.handoff?.standalone;
  if (standaloneDetails) {
    content.append(standaloneHandoff(standaloneDetails, "prepare"));
  }
  content.append(note, confirm);
}

async function monitorOperation(initial: Operation): Promise<void> {
  let operation = initial;
  const standalone = operation.kind === "standalone";
  const content = frame({
    eyebrow: standalone ? "Switching modes" : "Connecting",
    title: standalone ? "Starting standalone mode" : `Connecting to ${operation.network ?? "Wi-Fi"}`,
    description: standalone
      ? "The setup Wi-Fi will be replaced by the device’s standalone Wi-Fi. This captive window may close."
      : "The setup Wi-Fi will stop while the device connects. This captive window may close.",
  });
  const progress = element("div", "progress-card");
  const spinner = element("span", "spinner");
  spinner.setAttribute("aria-hidden", "true");
  const status = textElement("p", "Applying your choice…");
  status.setAttribute("role", "status");
  progress.append(spinner, status);
  content.append(
    progress,
    helpText(
      standalone
        ? "Join the standalone Wi-Fi when it appears. This page will retry automatically."
        : "Reconnect to your normal Wi-Fi if needed. This page will retry automatically.",
    ),
  );

  for (;;) {
    if (operation.state === "succeeded") {
      try {
        await refreshHandoff();
      } catch (error) {
        if (showLoginIfRequired(error)) return;
        throw error;
      }
      showComplete(operation);
      return;
    }
    if (operation.state === "failed") {
      showOperationFailure(operation);
      return;
    }
    await delay(1000);
    try {
      operation = await api.operation(operation.id);
      status.textContent = "Applying your choice…";
    } catch (error) {
      if (showLoginIfRequired(error)) return;
      status.textContent = "Waiting for the device to come back…";
    }
  }
}

async function refreshHandoff(): Promise<void> {
  const handoff = bootstrap.handoff;
  const application = handoff?.application;
  if (handoff && application) {
    bootstrap.handoff = {
      ...handoff,
      application: { label: application.label, ready: false },
    };
  }
  try {
    const refreshed = await api.bootstrap();
    bootstrap.handoff = refreshed.handoff;
  } catch (error) {
    if (authenticationRequired(error)) throw error;
    // The stable origin may still be reconnecting. The completion view continues
    // polling and must not expose a destination whose health could not be confirmed.
  }
}

function showOperationFailure(operation: Operation): void {
  const content = frame({
    eyebrow: "Try again",
    title: "That connection did not work",
    description: operation.failure?.message ?? "The device restored setup so you can try again.",
  });
  const actions = element("div", "row-actions");
  if (operation.kind === "connect") {
    actions.append(button("Choose a network", "button button-primary", () => void showNetworks()));
  } else {
    actions.append(button("Try standalone again", "button button-primary", showStandaloneConfirmation));
  }
  if (bootstrap.capabilities.network && bootstrap.capabilities.standalone) {
    actions.append(button("Change mode", "button button-quiet", showModeChoice));
  }
  content.append(actions);
}

function showComplete(operation: Operation): void {
  const standalone = operation.kind === "standalone";
  const content = frame({
    eyebrow: "Setup complete",
    title: standalone ? "Standalone mode is ready" : "The device is connected",
    description: standalone
      ? "You can keep using this device directly on its own Wi-Fi network."
      : "This device will now use the selected Wi-Fi network.",
  });
  const success = element("div", "success-mark");
  success.setAttribute("aria-hidden", "true");
  success.textContent = "✓";
  const actions = element("div", "row-actions");
  const application = bootstrap.handoff?.application;
  const standaloneDetails = standalone ? bootstrap.handoff?.standalone : undefined;
  const applicationStatus = application && !application.ready
    ? textElement("p", `${application.label} is still starting…`, "inline-status")
    : undefined;
  if (application?.ready && application.url) {
    actions.append(
      link(
        application.label,
        application.url,
        "button button-primary",
      ),
    );
  }
  actions.append(button("Change connection", "button button-secondary", showModeChoice));
  if (bootstrap.capabilities.network) {
    actions.append(
      button("Known networks", "button button-quiet", () => void showKnownNetworks()),
    );
  }
  content.prepend(success);
  if (applicationStatus) content.append(applicationStatus);
  if (standaloneDetails) {
    content.append(standaloneHandoff(standaloneDetails, "ready"));
  }
  content.append(
    actions,
    helpText(
      application
        ? "Open the device application, or return to setup whenever you need to change the connection."
        : "You can return to setup whenever you need to change the connection.",
    ),
  );
  if (applicationStatus) {
    void waitForApplication(viewRevision, applicationStatus, actions);
  }
}

function standaloneHandoff(
  details: StandaloneHandoff,
  stage: "prepare" | "ready",
): HTMLElement {
  const section = element("section", "standalone-handoff");
  section.append(
    textElement(
      "h2",
      stage === "prepare" ? "Save the standalone Wi-Fi details" : "Connect another device",
    ),
    textElement(
      "p",
      details.password
        ? stage === "prepare"
          ? "Keep these details available before the setup Wi-Fi is replaced."
          : "Scan the Wi-Fi code or join manually from another device."
        : "Join the standalone Wi-Fi manually using the network name below.",
      "handoff-description",
    ),
  );

  const manual = element("dl", "handoff-details");
  manual.append(detail("Wi-Fi name", details.ssid));
  if (details.password) {
    const passwordValue = element("span", "credential-value");
    passwordValue.textContent = details.password;
    const copy = button("Copy password", "button button-quiet copy-credential", () => {
      void copyText(details.password ?? "").then((copied) => {
        copy.textContent = copied ? "Password copied" : "Select and copy the password";
      });
    });
    manual.append(detailNode("Password", passwordValue, copy));
  }
  section.append(manual);

  if (details.password) {
    const codes = element("div", "qr-grid qr-grid-single");
    codes.append(qrCard("Join Wi-Fi", wifiQRPayload(details.ssid, details.password), details.ssid));
    section.append(codes);
  }
  return section;
}

function detail(label: string, value: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  fragment.append(textElement("dt", label), textElement("dd", value));
  return fragment;
}

function detailNode(label: string, ...values: Node[]): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const description = element("dd", "credential-detail");
  description.append(...values);
  fragment.append(textElement("dt", label), description);
  return fragment;
}

function qrCard(title: string, payload: string, caption: string): HTMLElement {
  const card = element("article", "qr-card");
  const canvas = document.createElement("canvas");
  canvas.setAttribute("role", "img");
  canvas.setAttribute("aria-label", title);
  const status = textElement("p", "Creating code…", "qr-status");
  card.append(textElement("strong", title), canvas, status, textElement("small", caption));
  void QRCode.toCanvas(canvas, payload, {
    width: 196,
    margin: 1,
    errorCorrectionLevel: "M",
    color: { dark: "#28151cff", light: "#ffffffff" },
  }).then(() => status.remove()).catch(() => {
    canvas.remove();
    status.textContent = "QR code unavailable. Use the details above.";
  });
  return card;
}

async function waitForApplication(
  revision: number,
  status: HTMLElement,
  actions: HTMLElement,
): Promise<void> {
  while (revision === viewRevision) {
    await delay(2000);
    try {
      const refreshed = await api.bootstrap();
      if (revision !== viewRevision) return;
      bootstrap.handoff = refreshed.handoff;
      const application = refreshed.handoff?.application;
      if (application?.ready && application.url) {
        actions.prepend(link(application.label, application.url, "button button-primary"));
        status.textContent = `${application.label} is ready.`;
        return;
      }
      status.textContent = application
        ? `${application.label} is still starting…`
        : "The application destination is not configured.";
    } catch (error) {
      if (revision !== viewRevision) return;
      if (showLoginIfRequired(error)) return;
      status.textContent = "Waiting for the device application…";
    }
  }
}

function authenticationRequired(error: unknown): boolean {
  return error instanceof APIError && error.code === "authentication_required";
}

function normalBrowserHandoff(): HTMLElement | undefined {
  const setupURL = bootstrap.handoff?.setup_url;
  if (!setupURL || !needsBrowserHandoff(window.location.href, setupURL)) return undefined;
  const panel = element("aside", "handoff-panel");
  panel.append(
    textElement("strong", "Keep setup open during the switch"),
    textElement(
      "p",
      "Open setup in your normal browser first. It can reconnect after this Wi-Fi connection changes.",
    ),
    link("Open setup in browser", setupURL, "button button-secondary"),
  );
  return panel;
}

async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // Plain HTTP and captive viewers often block the Clipboard API. Use the
    // user-gesture fallback below when the older selection command is available.
  }
  const input = document.createElement("textarea");
  input.value = value;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.append(input);
  input.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    input.remove();
  }
}

function showLoadFailure(error: unknown): void {
  const content = frame({
    eyebrow: "Setup unavailable",
    title: "We could not open setup",
    description: messageFrom(error),
  });
  content.append(button("Try again", "button button-primary", () => void start()));
}

function renderLoading(message: string): void {
  const wrap = element("div", "loading-screen");
  const spinner = element("span", "spinner");
  spinner.setAttribute("aria-hidden", "true");
  const status = textElement("p", message);
  status.setAttribute("role", "status");
  wrap.append(spinner, status);
  app.replaceChildren(wrap);
}

function showInlineError(parent: HTMLElement, message: string, retry: () => void): void {
  const panel = element("div", "error-panel");
  panel.setAttribute("role", "alert");
  panel.append(textElement("p", message), button("Try again", "button button-secondary", retry));
  parent.append(panel);
}

function choiceButton(
  title: string,
  description: string,
  badge: string,
  onClick: () => void,
): HTMLButtonElement {
  const control = button("", "choice-card", onClick);
  control.removeAttribute("aria-label");
  control.append(
    textElement("span", badge, "choice-badge"),
    textElement("strong", title),
    textElement("span", description, "choice-description"),
    textElement("span", "Continue →", "choice-action"),
  );
  return control;
}

function inputField(
  label: string,
  type: string,
  name: string,
  value: string,
  hint: string,
): { wrapper: HTMLElement; input: HTMLInputElement } {
  const wrapper = element("label", "field");
  wrapper.append(textElement("span", label, "field-label"));
  const input = document.createElement("input");
  input.type = type;
  input.name = name;
  input.value = value;
  input.required = true;
  input.spellcheck = false;
  wrapper.append(input, textElement("span", hint, "field-hint"));
  input.addEventListener("input", () => input.setCustomValidity(""));
  return { wrapper, input };
}

function selectField(
  label: string,
  name: string,
  choices: Array<[string, string]>,
): { wrapper: HTMLElement; select: HTMLSelectElement } {
  const wrapper = element("label", "field");
  wrapper.append(textElement("span", label, "field-label"));
  const control = element("span", "select-control");
  const select = document.createElement("select");
  select.name = name;
  for (const [value, copy] of choices) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = copy;
    select.append(option);
  }
  control.append(select);
  wrapper.append(control);
  return { wrapper, select };
}

function submitButton(label: string): HTMLButtonElement {
  const control = button(label, "button button-primary", () => undefined);
  control.type = "submit";
  return control;
}

function button(label: string, className: string, onClick: () => void): HTMLButtonElement {
  const control = document.createElement("button");
  control.type = "button";
  control.className = className;
  control.textContent = label;
  control.setAttribute("aria-label", label);
  control.addEventListener("click", onClick);
  return control;
}

function link(label: string, href: string, className: string): HTMLAnchorElement {
  const control = document.createElement("a");
  control.className = className;
  control.textContent = label;
  control.href = href;
  control.target = "_blank";
  control.rel = "noopener noreferrer";
  return control;
}

function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
): HTMLElementTagNameMap[K] {
  const result = document.createElement(tag);
  if (className) result.className = className;
  return result;
}

function textElement<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text: string,
  className?: string,
): HTMLElementTagNameMap[K] {
  const result = element(tag, className);
  result.textContent = text;
  return result;
}

function helpText(text: string): HTMLParagraphElement {
  return textElement("p", text, "help-text");
}

function messageFrom(error: unknown): string {
  if (error instanceof APIError || error instanceof Error) return error.message;
  return "The request could not be completed. Please try again.";
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}
