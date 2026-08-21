import "./styles.css";

import { APIError, SetupAPI } from "./api.ts";
import {
  initialView,
  modeLabel,
  strengthLabel,
  type Bootstrap,
  type Network,
  type Operation,
} from "./model.ts";

const appElement = document.querySelector<HTMLElement>("#app");
if (!appElement) throw new Error("setup root is missing");
const app: HTMLElement = appElement;

const api = new SetupAPI();
let bootstrap: Bootstrap;

void start();

async function start(): Promise<void> {
  renderLoading("Opening device setup…");
  try {
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
    showLoadFailure(error);
  }
}

function frame(options: {
  eyebrow: string;
  title: string;
  description: string;
  back?: () => void;
}): HTMLElement {
  const shell = element("section", "setup-shell");
  const header = element("header", "setup-header");
  if (options.back) {
    const back = button("Back", "button button-quiet", options.back);
    back.setAttribute("aria-label", "Go back");
    header.append(back);
  }
  const wordmark = element("div", "wordmark");
  const mark = element("span", "wordmark-mark");
  mark.setAttribute("aria-hidden", "true");
  for (let level = 1; level <= 4; level += 1) {
    mark.append(element("span", `wordmark-bar wordmark-bar-${level}`));
  }
  wordmark.append(mark, textElement("span", "Device setup"));
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
    title: "How should this device connect?",
    description: "Choose Wi-Fi for normal network access, or keep this device available as its own network.",
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
  content.append(choices, helpText("You can change this choice later."));
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
    );
    content.append(list, actions);
  } catch (error) {
    status.remove();
    showInlineError(content, messageFrom(error), () => void showNetworks());
  }
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
      confirm.disabled = false;
      showInlineError(content, messageFrom(error), showStandaloneConfirmation);
    }
  });
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
        ? "If this window closes, join the standalone Wi-Fi and reopen device setup in your normal browser."
        : "If this window closes, reconnect to your normal Wi-Fi and reopen setup from the device’s new network address.",
    ),
  );

  for (;;) {
    if (operation.state === "succeeded") {
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
    } catch {
      status.textContent = "Waiting for the device to come back…";
    }
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
  actions.append(button("Change connection", "button button-secondary", showModeChoice));
  content.prepend(success);
  content.append(actions, helpText("You can close this page."));
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
