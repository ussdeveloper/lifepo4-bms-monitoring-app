"use strict";

/* LiFePO4 BMS Monitor — front end.
   Polls /api/state once per second and updates the grid and the detail panel
   in place so hover state and animations stay stable. All labels are
   translated; English is the default, Polish can be selected in the title bar. */

const POLL_MS = 1000;
const RING_CIRCUMFERENCE = 2 * Math.PI * 52;
const LANGUAGE_STORAGE_KEY = "lifepo4-bms-monitor.language";

const STRINGS = {
  en: {
    "app.title": "LiFePO4 BMS Monitor",
    "app.subtitle": "battery panel",
    "window.minimize": "Minimize",
    "window.maximize": "Maximize / restore",
    "window.close": "Close",
    "lang.switch": "Switch language (English / Polish)",
    "grid.title": "Detected batteries",
    "grid.deepscan": "Check every device",
    "grid.refresh": "data refreshed every {n} s",
    "grid.progress": "waiting: {pending} · no BMS: {rejected}",
    "grid.saved": "saved batteries appear here even while they are asleep",
    "empty.title": "Looking for batteries…",
    "empty.hint": "If the BMS is asleep, press the wake-up button on the battery — it will show up here automatically.",
    "empty.inRange": "Bluetooth devices in range: {n}",
    "empty.noDevices": "No Bluetooth devices in range yet — check that Bluetooth is switched on.",
    "checking.queued": "{name} · queued",
    "checking.probing": "{name} · reading…",
    "state.bms": "BMS",
    "state.candidate": "queued",
    "state.probing": "reading",
    "state.notBMS": "no BMS",
    "state.unavailable": "no answer",
    "state.known": "saved",
    "state.offline": "offline",
    "detail.eyebrow": "Details",
    "detail.reading": "Reading from the BMS",
    "detail.selectPrompt": "Select a battery",
    "detail.hoverHint": "Hover a battery on the left. Data is read from its BMS every few seconds.",
    "detail.connecting": "Connecting to the battery and starting the reader…",
    "detail.retrying": "Battery is not answering — reconnecting (attempt {n})…",
    "detail.batteryFallback": "Battery",
    "metric.soc.short": "state of charge",
    "metric.voltage": "Voltage",
    "metric.current": "Current",
    "metric.power": "Power",
    "metric.powerCharging": "Charging power",
    "metric.powerDischarging": "Discharging power",
    "metric.powerWindow": "last 10 minutes",
    "metric.soh": "State of health",
    "metric.remaining": "Remaining",
    "metric.design": "Design capacity",
    "metric.cycles": "Cycles",
    "metric.model": "Model",
    "metric.cells": "Cells",
    "metric.temperatures": "Temperatures",
    "scan.starting": "starting Bluetooth…",
    "scan.active": "scan active",
    "scan.stopped": "scan stopped",
    "scan.unavailable": "Bluetooth unavailable",
    "scan.noService": "no connection to the service",
    "status.devices": "devices: {n}",
    "status.batteries": "batteries: {n}",
    "status.interval": "refresh every {n} s",
    "status.waiting": "waiting for a battery to be selected",
    "status.noBle": "no Bluetooth connection",
    "status.justNow": "last reading: just now",
    "status.ago": "last reading: {n} s ago",
    "status.samples": "history: {n} samples",
    "status.samplesEmpty": "history: empty",
    "settings.open": "Settings",
    "settings.close": "Close settings",
    "settings.eyebrow": "Configuration",
    "settings.title": "Settings",
    "settings.interval": "Refresh interval",
    "settings.intervalHint": "How often the monitored battery is read. The same value drives the CSV history.",
    "settings.api": "Read API",
    "settings.apiHint": "HTTP endpoint with the last known state of every battery, including batteries that are asleep right now.",
    "settings.mqtt": "MQTT and Home Assistant",
    "settings.mqttHint": "Publish readings to a broker and announce every battery to Home Assistant with MQTT discovery.",
    "settings.host": "Broker host",
    "settings.port": "Port",
    "settings.username": "User",
    "settings.password": "Password",
    "settings.prefix": "Topic prefix",
    "settings.discoveryPrefix": "Discovery prefix",
    "settings.discovery": "Home Assistant discovery",
    "settings.discoveryHint": "Each battery becomes one device with sensors for state of charge, voltage, current, cells, temperatures and flags.",
    "settings.save": "Save",
    "settings.saved": "saved",
    "settings.saving": "saving…",
    "settings.apiDisabled": "The read API is disabled.",
    "settings.apiUrl": "GET {url} (add ?addr=4E:DF:… for a single battery)",
    "charge.charging": "charging",
    "charge.discharging": "discharging",
    "charge.idle": "idle (no current)",
    "status.mqtt": "MQTT {state}",
    "status.mqttOn": "connected",
    "status.mqttOff": "off",
    "card.battery": "Battery",
    "card.offline": "not in range · seen {n} min ago",
    "card.offlineNow": "not in range",
    "flags.chargingMosfet": "Charging MOSFET",
    "flags.dischargingMosfet": "Discharging MOSFET",
    "flags.balancer": "Balancing",
    "flags.noProblems": "no problems",
    "flags.problems": "problem codes: 0x{code}",
    "cells.summary": "{count} cells · min {min} V · max {max} V · spread {delta} mV",
    "temp.sensor": "sensor {index}: {value} °C",
    "error.noDevice": "The battery is not in range — wake it up and try again.",
    "error.connect": "Could not connect to the battery.",
    "error.notify": "The BMS did not accept notifications.",
    "error.auth": "The BMS rejected the authentication.",
    "error.protocol": "The battery answered in an unexpected way.",
    "error.unsupportedDevice": "This device does not speak a supported BMS protocol.",
    "error.ble": "Bluetooth library error.",
    "error.generic": "Reading the battery failed.",
  },
  pl: {
    "app.title": "LiFePO4 BMS Monitor",
    "app.subtitle": "panel akumulatorów",
    "window.minimize": "Minimalizuj",
    "window.maximize": "Maksymalizuj / przywróć",
    "window.close": "Zamknij",
    "lang.switch": "Zmień język (angielski / polski)",
    "grid.title": "Wykryte akumulatory",
    "grid.deepscan": "Sprawdź wszystkie urządzenia",
    "grid.refresh": "dane odświeżane co {n} s",
    "grid.progress": "w kolejce: {pending} · nie BMS: {rejected}",
    "grid.saved": "zapisane akumulatory pojawiają się tutaj także gdy śpią",
    "empty.title": "Szukam akumulatorów…",
    "empty.hint": "Jeśli BMS śpi, naciśnij przycisk wybudzania na akumulatorze — pojawi się na liście automatycznie.",
    "empty.inRange": "Urządzenia Bluetooth w zasięgu: {n}",
    "empty.noDevices": "Brak urządzeń Bluetooth w zasięgu — sprawdź, czy Bluetooth jest włączony.",
    "checking.queued": "{name} · w kolejce",
    "checking.probing": "{name} · odczyt…",
    "state.bms": "BMS",
    "state.candidate": "w kolejce",
    "state.probing": "odczyt",
    "state.notBMS": "nie BMS",
    "state.unavailable": "brak odpowiedzi",
    "state.known": "zapisany",
    "state.offline": "offline",
    "detail.eyebrow": "Szczegóły",
    "detail.reading": "Odczyt z BMS",
    "detail.selectPrompt": "Wybierz akumulator",
    "detail.hoverHint": "Najedź kursorem na akumulator po lewej stronie. Dane są odczytywane z BMS co kilka sekund.",
    "detail.connecting": "Łączę się z akumulatorem i uruchamiam odczyt…",
    "detail.retrying": "Akumulator nie odpowiada — ponawiam połączenie (próba {n})…",
    "detail.batteryFallback": "Akumulator",
    "metric.soc.short": "naładowanie",
    "metric.voltage": "Napięcie",
    "metric.current": "Prąd",
    "metric.power": "Moc",
    "metric.powerCharging": "Moc ładowania",
    "metric.powerDischarging": "Moc rozładowania",
    "metric.powerWindow": "ostatnie 10 minut",
    "metric.soh": "Stan zdrowia",
    "metric.remaining": "Pozostało",
    "metric.design": "Pojemność projektowa",
    "metric.cycles": "Cykle",
    "metric.model": "Model",
    "metric.cells": "Ogniwa",
    "metric.temperatures": "Temperatury",
    "scan.starting": "uruchamiam Bluetooth…",
    "scan.active": "skanowanie aktywne",
    "scan.stopped": "skanowanie zatrzymane",
    "scan.unavailable": "Bluetooth niedostępny",
    "scan.noService": "brak połączenia z usługą",
    "status.devices": "urządzeń: {n}",
    "status.batteries": "akumulatory: {n}",
    "status.interval": "odświeżanie co {n} s",
    "status.waiting": "oczekuję na wybór akumulatora",
    "status.noBle": "brak połączenia BLE",
    "status.justNow": "ostatni odczyt: przed chwilą",
    "status.ago": "ostatni odczyt: {n} s temu",
    "status.samples": "historia: {n} próbek",
    "status.samplesEmpty": "historia: brak danych",
    "settings.open": "Ustawienia",
    "settings.close": "Zamknij ustawienia",
    "settings.eyebrow": "Konfiguracja",
    "settings.title": "Ustawienia",
    "settings.interval": "Częstotliwość odświeżania",
    "settings.intervalHint": "Jak często odczytywany jest monitorowany akumulator. Ta wartość steruje także zapisem historii CSV.",
    "settings.api": "API odczytu",
    "settings.apiHint": "Punkt HTTP z ostatnim znanym stanem każdego akumulatora, także gdy akumulator teraz śpi.",
    "settings.mqtt": "MQTT i Home Assistant",
    "settings.mqttHint": "Wysyłaj odczyty do brokera i zgłaszaj każdy akumulator do Home Assistant przez MQTT discovery.",
    "settings.host": "Adres brokera",
    "settings.port": "Port",
    "settings.username": "Użytkownik",
    "settings.password": "Hasło",
    "settings.prefix": "Prefiks tematów",
    "settings.discoveryPrefix": "Prefiks discovery",
    "settings.discovery": "Discovery dla Home Assistant",
    "settings.discoveryHint": "Każdy akumulator staje się urządzeniem z czujnikami naładowania, napięcia, prądu, ogniw, temperatur i flag.",
    "settings.save": "Zapisz",
    "settings.saved": "zapisano",
    "settings.saving": "zapisuję…",
    "settings.apiDisabled": "API odczytu jest wyłączone.",
    "settings.apiUrl": "GET {url} (dodaj ?addr=4E:DF:… dla jednego akumulatora)",
    "charge.charging": "ładowanie",
    "charge.discharging": "rozładowanie",
    "charge.idle": "bez prądu",
    "status.mqtt": "MQTT {state}",
    "status.mqttOn": "połączone",
    "status.mqttOff": "wyłączone",
    "card.battery": "Akumulator",
    "card.offline": "poza zasięgiem · widziany {n} min temu",
    "card.offlineNow": "poza zasięgiem",
    "flags.chargingMosfet": "MOSFET ładowania",
    "flags.dischargingMosfet": "MOSFET rozładowania",
    "flags.balancer": "Balansowanie",
    "flags.noProblems": "brak problemów",
    "flags.problems": "kody problemów: 0x{code}",
    "cells.summary": "{count} ogniw · min {min} V · max {max} V · różnica {delta} mV",
    "temp.sensor": "czujnik {index}: {value} °C",
    "error.noDevice": "Akumulator jest poza zasięgiem — wybudź go i spróbuj ponownie.",
    "error.connect": "Nie udało się połączyć z akumulatorem.",
    "error.notify": "BMS nie przyjął powiadomień.",
    "error.auth": "BMS odrzucił uwierzytelnianie.",
    "error.protocol": "Akumulator odpowiedział w nieoczekiwany sposób.",
    "error.unsupportedDevice": "To urządzenie nie używa obsługiwanego protokołu BMS.",
    "error.ble": "Błąd biblioteki Bluetooth.",
    "error.generic": "Odczyt akumulatora nie powiódł się.",
  },
};

let language = "en";
let state = null;
let pinnedAddress = null;
let lastRequested = "";
let hoverTimer = null;
let lastCardSignature = "";
let lastLanguage = "";
let lastUpdatedAgo = null;

const el = {
  scanText: document.getElementById("scan-text"),
  batteryChip: document.getElementById("battery-chip"),
  langToggle: document.getElementById("lang-toggle"),
  langLabel: document.getElementById("lang-label"),
  statusScan: document.getElementById("status-scan"),
  statusDot: document.getElementById("status-dot"),
  statusSeen: document.getElementById("status-seen"),
  statusBatteries: document.getElementById("status-batteries"),
  statusInterval: document.getElementById("status-interval"),
  statusHistory: document.getElementById("status-history"),
  statusUpdated: document.getElementById("status-updated"),
  statusVersion: document.getElementById("status-version"),
  cards: document.getElementById("cards"),
  cardsScroll: document.getElementById("cards-scroll"),
  checkingCaption: document.getElementById("checking-caption"),
  empty: document.getElementById("empty"),
  checking: document.getElementById("checking"),
  deepscan: document.getElementById("deepscan"),
  refreshNote: document.getElementById("refresh-note"),
  scanSummary: document.getElementById("scan-summary"),
  detailEyebrow: document.getElementById("detail-eyebrow"),
  detailName: document.getElementById("detail-name"),
  detailAddr: document.getElementById("detail-addr"),
  detailProto: document.getElementById("detail-proto"),
  detailBrand: document.getElementById("detail-brand"),
  detailPlaceholder: document.getElementById("detail-placeholder"),
  detailContent: document.getElementById("detail-content"),
  detailError: document.getElementById("detail-error"),
  errorHeadline: document.getElementById("error-headline"),
  errorDetail: document.getElementById("error-detail"),
  historyLine: document.getElementById("history-line"),
  statusMqtt: document.getElementById("status-mqtt"),
  settingsOverlay: document.getElementById("settings-overlay"),
  settingsToggle: document.getElementById("settings-toggle"),
  settingsClose: document.getElementById("settings-close"),
  settingsSave: document.getElementById("settings-save"),
  settingsPath: document.getElementById("settings-path"),
  settingsState: document.getElementById("settings-state"),
  intervalRange: document.getElementById("interval-range"),
  intervalCustom: document.getElementById("interval-custom"),
  apiToggle: document.getElementById("api-toggle"),
  apiHint: document.getElementById("api-hint"),
  mqttEnabled: document.getElementById("mqtt-enabled"),
  mqttHost: document.getElementById("mqtt-host"),
  mqttPort: document.getElementById("mqtt-port"),
  mqttUser: document.getElementById("mqtt-user"),
  mqttPassword: document.getElementById("mqtt-password"),
  mqttPrefix: document.getElementById("mqtt-prefix"),
  mqttDiscoveryPrefix: document.getElementById("mqtt-discovery-prefix"),
  mqttDiscovery: document.getElementById("mqtt-discovery"),
  ringValue: document.getElementById("ring-value"),
  socValue: document.getElementById("soc-value"),
  mVoltage: document.getElementById("m-voltage"),
  mCurrent: document.getElementById("m-current"),
  mSoh: document.getElementById("m-soh"),
  mRemaining: document.getElementById("m-remaining"),
  mDesign: document.getElementById("m-design"),
  mCycles: document.getElementById("m-cycles"),
  mModel: document.getElementById("m-model"),
  cycleFill: document.getElementById("cycle-fill"),
  powerMetric: document.getElementById("power-metric"),
  powerLabel: document.getElementById("power-label"),
  mPower: document.getElementById("m-power"),
  powerLine: document.getElementById("power-line"),
  powerArea: document.getElementById("power-area"),
  powerDot: document.getElementById("power-dot"),
  flags: document.getElementById("flags"),
  cells: document.getElementById("cells"),
  cellsSummary: document.getElementById("cells-summary"),
  temps: document.getElementById("temps"),
};

/* --- charging state icon (Lucide "zap" / "arrow-down" / "minus", light stroke) --- */

const CHARGE_ICONS = {
  charging: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/></svg>',
  discharging: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14"/><path d="m19 12-7 7-7-7"/></svg>',
  idle: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14"/></svg>',
};

// chargeState classifies a reading: positive current means charging.
function chargeState(sample) {
  if (!sample) return "idle";
  const current = Number(sample.currentA) || 0;
  if (current > 0.05) return "charging";
  if (current < -0.05) return "discharging";
  return "idle";
}

function chargeIconMarkup() {
  return `<span class="charge-icon idle" data-charge="idle">${CHARGE_ICONS.idle}</span>`;
}

/* --- translation helpers --- */

function t(key, vars) {
  const table = STRINGS[language] || STRINGS.en;
  let text = table[key] !== undefined ? table[key] : (STRINGS.en[key] !== undefined ? STRINGS.en[key] : key);
  if (vars) {
    text = text.replace(/\{(\w+)\}/g, (match, name) =>
      vars[name] !== undefined ? String(vars[name]) : match);
  }
  return text;
}

function applyStaticTranslations() {
  document.documentElement.lang = language;
  document.querySelectorAll("[data-i18n]").forEach((node) => {
    node.textContent = t(node.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-title]").forEach((node) => {
    const text = t(node.dataset.i18nTitle);
    node.title = text;
    node.setAttribute("aria-label", text);
  });
  el.langLabel.textContent = language === "en" ? "PL" : "EN";
}

function setLanguage(next) {
  language = next === "pl" ? "pl" : "en";
  try {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, language);
  } catch (error) {
    /* private mode — ignore */
  }
  lastCardSignature = "";
  applyStaticTranslations();
  if (state) {
    renderCards();
    renderChecking();
    renderDetail();
    renderChrome();
  }
}

function initialLanguage(serverDefault) {
  try {
    const stored = localStorage.getItem(LANGUAGE_STORAGE_KEY);
    if (stored === "pl" || stored === "en") return stored;
  } catch (error) {
    /* private mode — ignore */
  }
  return serverDefault === "pl" ? "pl" : "en";
}

/* --- small utilities --- */

function escapeHtml(text) {
  return String(text ?? "").replace(/[&<>"']/g, (character) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[character]));
}

function fixed(value, digits) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toFixed(digits) : "–";
}

function protocolBadgeClass(protocol) {
  switch (protocol) {
    case "HS03": return "hs03";
    case "HS04": return "hs04";
    case "JBD": return "jbd";
    default: return "";
  }
}

function cssEscape(value) {
  if (window.CSS && typeof window.CSS.escape === "function") {
    return window.CSS.escape(value);
  }
  return String(value).replace(/[^a-zA-Z0-9:._-]/g, "\\$&");
}

function minutesSince(timestamp) {
  const seen = Date.parse(timestamp);
  if (!Number.isFinite(seen)) return null;
  return Math.max(0, Math.round((Date.now() - seen) / 60000));
}

/* --- window chrome (custom title bar) --- */

function windowAction(action) {
  return fetch("/api/window", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ action }),
  }).catch(() => {});
}

document.querySelectorAll(".titlebar-buttons .win-button").forEach((button) => {
  button.addEventListener("click", (event) => {
    event.stopPropagation();
    windowAction(button.dataset.action);
  });
});

el.langToggle.addEventListener("mousedown", (event) => event.stopPropagation());
el.langToggle.addEventListener("click", (event) => {
  event.stopPropagation();
  setLanguage(language === "en" ? "pl" : "en");
});

const titlebar = document.getElementById("titlebar");
titlebar.addEventListener("mousedown", (event) => {
  if (event.button !== 0) return;
  if (event.target.closest(".win-button")) return;
  if (event.target.closest(".lang-button")) return;
  if (event.target.closest(".icon-button")) return;
  windowAction("drag");
});
titlebar.addEventListener("dblclick", (event) => {
  if (event.target.closest(".win-button")) return;
  if (event.target.closest(".lang-button")) return;
  if (event.target.closest(".icon-button")) return;
  windowAction("maximize");
});

/* --- settings panel --- */

function openSettings() {
  fillSettings();
  el.settingsOverlay.hidden = false;
}

function closeSettings() {
  el.settingsOverlay.hidden = true;
}

function fillSettings() {
  const settings = (state && state.config && state.config.settings) || null;
  if (!settings) return;
  const interval = Number(settings.intervalS) || 5;
  el.intervalRange.value = String(Math.min(30, Math.max(2, Math.round(interval))));
  el.intervalCustom.value = String(interval);
  el.apiToggle.checked = Boolean(settings.apiEnabled);
  el.mqttEnabled.checked = Boolean(settings.mqtt && settings.mqtt.enabled);
  el.mqttHost.value = (settings.mqtt && settings.mqtt.host) || "";
  el.mqttPort.value = String((settings.mqtt && settings.mqtt.port) || 1883);
  el.mqttUser.value = (settings.mqtt && settings.mqtt.username) || "";
  el.mqttPassword.value = (settings.mqtt && settings.mqtt.password) || "";
  el.mqttPrefix.value = (settings.mqtt && settings.mqtt.topicPrefix) || "";
  el.mqttDiscoveryPrefix.value = (settings.mqtt && settings.mqtt.discoveryPrefix) || "";
  el.mqttDiscovery.checked = Boolean(settings.mqtt && settings.mqtt.discovery);
  el.settingsPath.textContent = settings.path || "";
  updateApiHint();
  renderIntervalSlider();
}

function updateApiHint() {
  if (!el.apiToggle.checked) {
    el.apiHint.textContent = t("settings.apiDisabled");
    return;
  }
  el.apiHint.textContent = t("settings.apiUrl", { url: `${location.origin}/api/batteries` });
}

async function saveSettings() {
  el.settingsState.textContent = t("settings.saving");
  const interval = Number(el.intervalCustom.value);
  const patch = {
    intervalS: Number.isFinite(interval) && interval > 0 ? interval : undefined,
    apiEnabled: el.apiToggle.checked,
    mqtt: {
      enabled: el.mqttEnabled.checked,
      host: el.mqttHost.value.trim(),
      port: Number(el.mqttPort.value) || 1883,
      username: el.mqttUser.value.trim(),
      password: el.mqttPassword.value,
      topicPrefix: el.mqttPrefix.value.trim(),
      discovery: el.mqttDiscovery.checked,
      discoveryPrefix: el.mqttDiscoveryPrefix.value.trim(),
    },
  };
  try {
    const response = await fetch("/api/settings", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const view = await response.json();
    if (state && state.config) {
      state.config.settings = view;
      state.config.intervalS = view.intervalS;
    }
    fillSettings();
    renderChrome();
    el.settingsState.textContent = t("settings.saved");
    setTimeout(() => { el.settingsState.textContent = ""; }, 2500);
  } catch (error) {
    el.settingsState.textContent = String(error);
  }
}

el.settingsToggle.addEventListener("mousedown", (event) => event.stopPropagation());
el.settingsToggle.addEventListener("click", (event) => {
  event.stopPropagation();
  if (el.settingsOverlay.hidden) {
    openSettings();
  } else {
    closeSettings();
  }
});
el.settingsClose.addEventListener("click", closeSettings);
el.settingsSave.addEventListener("click", saveSettings);
el.settingsOverlay.addEventListener("mousedown", (event) => {
  if (event.target === el.settingsOverlay) closeSettings();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !el.settingsOverlay.hidden) closeSettings();
});
function renderIntervalSlider() {
  const minimum = Number(el.intervalRange.min) || 2;
  const maximum = Number(el.intervalRange.max) || 30;
  const value = Number(el.intervalRange.value) || minimum;
  const fill = ((value - minimum) / Math.max(1, maximum - minimum)) * 100;
  el.intervalRange.style.setProperty("--fill", `${Math.max(2, Math.min(100, fill))}%`);
}

el.intervalRange.addEventListener("input", () => {
  el.intervalCustom.value = el.intervalRange.value;
  renderIntervalSlider();
});
el.intervalCustom.addEventListener("input", () => {
  const value = Number(el.intervalCustom.value);
  if (Number.isFinite(value) && value >= 2 && value <= 30) {
    el.intervalRange.value = String(Math.round(value));
    renderIntervalSlider();
  }
});
el.apiToggle.addEventListener("change", updateApiHint);

/* --- battery cards --- */

function cardSignature(batteries) {
  return batteries
    .map((battery) => [battery.addr, battery.name, battery.protocol, battery.brand,
      battery.online, battery.status].join("|"))
    .join("#");
}

// displayLabel is what the interface shows for a device: its name when it has
// one, otherwise the label Windows uses itself ("Bluetooth AA:BB:CC:DD:EE:FF").
function displayLabel(device) {
  const name = String(device.name || "").trim();
  if (name) return name;
  return `Bluetooth ${String(device.addr || "").toLowerCase()}`;
}

function renderCards() {
  const batteries = state.hub.batteries || [];
  const signature = cardSignature(batteries);
  if (signature !== lastCardSignature) {
    lastCardSignature = signature;
    el.cards.innerHTML = batteries
      .map((battery) => {
        const name = displayLabel(battery);
        const protocol = battery.protocol || "BMS";
        const offline = battery.online ? "" : " offline";
        return `
          <article class="card glass${offline}" data-addr="${escapeHtml(battery.addr)}">
            <div class="card-ident">
              <div class="card-title">
                <span class="card-name">${escapeHtml(name)}</span>
                ${chargeIconMarkup()}
              </div>
              <span class="card-addr mono">${escapeHtml(battery.addr)}</span>
            </div>
            <span class="chip badge ${protocolBadgeClass(battery.protocol)}">${escapeHtml(protocol)}</span>
            <div class="card-metrics">
              <div class="soc-bar"><span style="width:0%"></span></div>
              <div class="card-soc">–</div>
            </div>
            <div class="card-foot">
              <span class="card-live"></span>
              <span class="card-meta"></span>
            </div>
          </article>`;
      })
      .join("");
    wireCards();
  }

  // Values change every second: update them in place instead of re-rendering
  // the list, so hovering stays stable.
  for (const battery of batteries) {
    const card = el.cards.querySelector(`.card[data-addr="${cssEscape(battery.addr)}"]`);
    if (!card) continue;
    const live = state.session.addr === battery.addr ? state.session.sample : null;
    const sessionActive = state.session.addr === battery.addr && state.session.status === "connected";
    const online = Boolean(battery.online) || sessionActive;
    const socValue = live ? Number(live.soc) : NaN;
    const hasSoc = Number.isFinite(socValue);
    card.querySelector(".card-soc").textContent = hasSoc ? `${Math.round(socValue)}%` : "–";
    card.querySelector(".soc-bar span").style.width = hasSoc
      ? `${Math.max(2, Math.min(100, socValue))}%`
      : "0%";

    const detail = card.querySelector(".card-live");
    if (live) {
      detail.textContent = `${fixed(live.voltageV, 2)} V · ${live.currentA >= 0 ? "+" : ""}${fixed(live.currentA, 1)} A`;
    } else if (battery.model && battery.brand) {
      detail.textContent = `${battery.model} · ${battery.brand}`;
    } else {
      detail.textContent = battery.model || battery.brand || t("card.battery");
    }

    const icon = card.querySelector(".charge-icon");
    if (icon) {
      const state = online ? chargeState(live) : "idle";
      if (icon.dataset.charge !== state) {
        icon.dataset.charge = state;
        icon.className = `charge-icon ${state}`;
        icon.innerHTML = CHARGE_ICONS[state];
      }
      icon.title = `${t("card.battery")}: ${t(`charge.${state}`)}`;
    }

    const meta = card.querySelector(".card-meta");
    if (online) {
      meta.textContent = `RSSI ${battery.rssi} dBm`;
    } else {
      const minutes = minutesSince(battery.lastSeen);
      meta.textContent = minutes === null || minutes > 24 * 60
        ? t("card.offlineNow")
        : t("card.offline", { n: minutes });
    }
    card.classList.toggle("offline", !online);
    card.classList.toggle("pinned", battery.addr === pinnedAddress);
  }

  el.empty.hidden = batteries.length > 0;
  el.cardsScroll.hidden = batteries.length === 0;
  el.cards.hidden = batteries.length === 0;
}

function wireCards() {
  el.cards.querySelectorAll(".card").forEach((card) => {
    const address = card.dataset.addr;
    card.addEventListener("mouseenter", () => {
      clearTimeout(hoverTimer);
      hoverTimer = setTimeout(() => chooseDevice(address), 160);
    });
    card.addEventListener("click", () => {
      pinnedAddress = address;
      chooseDevice(address);
      renderCards();
    });
  });
}

function chooseDevice(address) {
  if (!address || address === lastRequested) return;
  lastRequested = address;
  fetch("/api/select", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ addr: address }),
  }).catch(() => {});
}

function renderChecking() {
  const devices = state.hub.devices || [];
  if (!devices.length) {
    el.checkingCaption.textContent = t("empty.noDevices");
    el.checking.innerHTML = "";
    return;
  }
  const rows = devices
    .slice(0, 30)
    .map((device) => {
      const status = device.status === "known" ? "known" : device.status;
      const meta = device.online ? `RSSI ${device.rssi} dBm` : t("state.offline");
      return `<div class="device-row">
        <span class="device-name">${escapeHtml(displayLabel(device))}</span>
        <span class="chip small ${deviceStateClass(status)}">${escapeHtml(t(`state.${status}`))}</span>
        <span class="device-rssi mono">${escapeHtml(meta)}</span>
      </div>`;
    })
    .join("");
  el.checkingCaption.textContent = t("empty.inRange", { n: devices.length });
  el.checking.innerHTML = rows;
}

// deviceStateClass maps a scanner status to a colour hint in the device list.
function deviceStateClass(status) {
  switch (status) {
    case "bms":
    case "known":
      return "state-ok";
    case "probing":
    case "candidate":
      return "state-pending";
    case "unavailable":
      return "state-warn";
    default:
      return "state-muted";
  }
}

function stateDeviceName(address) {
  const all = [...(state.hub.batteries || []), ...(state.hub.checking || [])];
  const found = all.find((device) => device.addr === address);
  return found ? found.name : "";
}

/* --- detail panel --- */

function renderDetail() {
  const session = state.session || {};
  const sample = session.sample;

  el.detailEyebrow.textContent = session.status === "connected" ? t("detail.reading") : t("detail.eyebrow");
  el.detailName.textContent = session.addr
    ? (stateDeviceName(session.addr) || t("detail.batteryFallback"))
    : t("detail.selectPrompt");
  el.detailAddr.textContent = session.addr || "\u00a0";

  const badge = (node, value) => {
    if (value) {
      node.hidden = false;
      node.textContent = value;
    } else {
      node.hidden = true;
    }
  };
  badge(el.detailProto, session.protocol);
  badge(el.detailBrand, session.brand);

  const hasData = Boolean(sample && session.status === "connected");
  el.detailPlaceholder.hidden = hasData || session.status === "connecting" || session.status === "connected";
  el.detailContent.hidden = !hasData;

  if (session.status === "error" && session.error) {
    el.detailError.hidden = false;
    el.errorHeadline.textContent = t(`error.${session.errorCode || "generic"}`);
    el.errorDetail.textContent = session.error;
  } else {
    el.detailError.hidden = true;
  }

  if (!hasData) {
    if (session.status === "connecting") {
      el.detailPlaceholder.hidden = false;
      el.detailPlaceholder.innerHTML = `<p>${escapeHtml(t("detail.connecting"))}</p>`;
    } else if (session.addr && session.status === "error") {
      // Retrying in the background: show how often we already tried.
      el.detailPlaceholder.hidden = false;
      const attempt = Number(session.attempt) || 0;
      el.detailPlaceholder.innerHTML = `<p>${escapeHtml(attempt > 1
        ? t("detail.retrying", { n: attempt })
        : t("detail.connecting"))}</p>`;
    }
    lastUpdatedAgo = null;
    return;
  }

  lastUpdatedAgo = Number(session.updatedAgoS) || 0;

  const soc = Math.max(0, Math.min(100, Number(sample.soc) || 0));
  el.socValue.textContent = `${Math.round(soc)}%`;
  el.ringValue.style.strokeDashoffset = String(RING_CIRCUMFERENCE * (1 - soc / 100));

  el.mVoltage.textContent = `${fixed(sample.voltageV, 2)} V`;
  const current = Number(sample.currentA) || 0;
  el.mCurrent.textContent = `${current >= 0 ? "+" : ""}${fixed(current, 1)} A`;
  el.mCurrent.style.color = current > 0.05 ? "#0d8f8f" : current < -0.05 ? "#b45f00" : "inherit";
  el.mSoh.textContent = sample.soh != null ? `${sample.soh}%` : "–";

  el.mRemaining.textContent = `${fixed(sample.remainingAh, 1)} Ah`;
  el.mDesign.textContent = `${fixed(sample.designAh, 1)} Ah`;
  el.mCycles.textContent = sample.cycles != null ? String(sample.cycles) : "–";
  el.mModel.textContent = session.model || "–";

  const interval = Number(state.config.intervalS) || 5;
  el.cycleFill.style.width = `${Math.min(100, (lastUpdatedAgo / interval) * 100)}%`;

  renderPower(session, sample);

  const samples = Number(session.samples) || 0;
  if (samples > 0) {
    el.historyLine.hidden = false;
    el.historyLine.textContent = t("status.samples", { n: samples });
    el.historyLine.title = state.config.dataDir || "";
  } else {
    el.historyLine.hidden = true;
  }

  renderFlags(sample);
  renderCells(sample);
  renderTemps(sample);
}

/* --- charging power graph (last ten minutes) --- */

const POWER_WINDOW_MS = 10 * 60 * 1000;

// renderPower shows how much power is going in or out of the battery, with a
// small graph of the last ten minutes.
function renderPower(session, sample) {
  const current = Number(sample.currentA) || 0;
  const watts = (Number(sample.voltageV) || 0) * current;
  const charging = current > 0.05;
  const discharging = current < -0.05;

  el.powerMetric.classList.toggle("charging", charging);
  el.powerMetric.classList.toggle("discharging", discharging);
  el.powerLabel.textContent = charging
    ? t("metric.powerCharging")
    : discharging ? t("metric.powerDischarging") : t("metric.power");
  el.mPower.textContent = charging || discharging
    ? `${charging ? "+" : "−"}${fixed(Math.abs(watts), 0)} W`
    : `${fixed(watts, 0)} W`;

  const spark = buildSpark((session && session.power) || []);
  el.powerLine.setAttribute("d", spark.line);
  el.powerArea.setAttribute("d", spark.area);
  el.powerDot.setAttribute("cx", spark.dot ? spark.dot[0].toFixed(2) : "-10");
  el.powerDot.setAttribute("cy", spark.dot ? spark.dot[1].toFixed(2) : "-10");
}

// buildSpark converts readings into a path for the 120x34 sparkline. The window
// is always ten minutes wide, so the line only starts at the left edge once the
// readings reach that far back.
function buildSpark(points) {
  const viewWidth = 120;
  const viewHeight = 34;
  const padding = 3;
  const stamp = Date.now();
  const newest = points.length ? points[points.length - 1].t : stamp;
  const end = Math.max(stamp, newest);
  const start = end - POWER_WINDOW_MS;
  const visible = points.filter((point) => point.t >= start && point.t <= end);
  if (!visible.length) {
    return { line: "", area: "", dot: null };
  }

  const values = visible.map((point) => Number(point.w) || 0);
  const low = Math.min(...values);
  const high = Math.max(...values);
  const middle = (low + high) / 2;
  // Keep a minimum vertical range so a steady reading does not look dramatic.
  const span = Math.max(high - low, Math.max(Math.abs(high), 1) * 0.08, 1);
  const top = middle + span / 2;
  const bottom = middle - span / 2;
  const positionX = (value) => ((value - start) / POWER_WINDOW_MS) * viewWidth;
  const positionY = (value) => padding + (1 - (value - bottom) / (top - bottom)) * (viewHeight - padding * 2);

  const coords = visible.map((point, index) => [positionX(point.t), positionY(values[index])]);
  if (coords.length === 1) {
    // A single reading has no line yet, but the value is already marked.
    return { line: "", area: "", dot: coords[0] };
  }
  const line = coords
    .map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(2)} ${y.toFixed(2)}`)
    .join(" ");
  const first = coords[0];
  const last = coords[coords.length - 1];
  const area = `${line} L${last[0].toFixed(2)} ${viewHeight} L${first[0].toFixed(2)} ${viewHeight} Z`;
  return { line, area, dot: last };
}

function renderFlags(sample) {
  const flags = [];
  const flag = (label, on) => {
    flags.push(`<span class="flag ${on ? "on" : "off"}">${escapeHtml(label)}</span>`);
  };
  if (sample.chargingMosfet != null) flag(t("flags.chargingMosfet"), sample.chargingMosfet);
  if (sample.dischargingMosfet != null) flag(t("flags.dischargingMosfet"), sample.dischargingMosfet);
  if (sample.balancer != null) flag(t("flags.balancer"), sample.balancer);
  if (sample.problemCode != null) {
    const problems = Number(sample.problemCode);
    flags.push(
      `<span class="flag ${problems === 0 ? "on" : "warn"}">` +
      `${problems === 0 ? escapeHtml(t("flags.noProblems")) : escapeHtml(t("flags.problems", { code: problems.toString(16).toUpperCase() }))}</span>`
    );
  }
  el.flags.innerHTML = flags.join("");
}

function renderCells(sample) {
  const cells = sample.cellsMv || [];
  if (!cells.length) {
    el.cells.innerHTML = "";
    el.cellsSummary.textContent = "";
    return;
  }
  const minimum = Math.min(...cells);
  const maximum = Math.max(...cells);
  el.cellsSummary.textContent = t("cells.summary", {
    count: cells.length,
    min: fixed(minimum / 1000, 3),
    max: fixed(maximum / 1000, 3),
    delta: maximum - minimum,
  });

  const lowerBound = minimum - 20;
  const span = Math.max(1, maximum - lowerBound);
  el.cells.innerHTML = cells
    .map((millivolts, index) => {
      const width = Math.max(4, Math.min(100, ((millivolts - lowerBound) / span) * 100));
      const low = maximum - millivolts >= 25 ? " low" : "";
      return `<div class="cell-row${low}">
        <span class="muted">${String(index + 1).padStart(2, "0")}</span>
        <div class="cell-track"><span style="width:${width}%"></span></div>
        <span class="cell-value">${fixed(millivolts / 1000, 3)} V</span>
      </div>`;
    })
    .join("");
}

function renderTemps(sample) {
  const temperatures = sample.temperaturesC || [];
  el.temps.innerHTML = temperatures
    .map((temperature, index) => {
      const hot = temperature >= 45;
      return `<span class="flag ${hot ? "warn" : "off"}">${escapeHtml(t("temp.sensor", {
        index: index + 1,
        value: fixed(temperature, 1),
      }))}</span>`;
    })
    .join("");
}

/* --- title bar, empty state and status bar --- */

function renderChrome() {
  const seen = state.hub.seen || 0;
  const batteries = (state.hub.batteries || []).length;
  const interval = Math.round(Number(state.config.intervalS) || 5);

  const scanning = Boolean(state.hub.scanning);
  const enabled = Boolean(state.hub.enabled);
  const scanLabel = !enabled
    ? (state.hub.enableError ? t("scan.unavailable") : t("scan.starting"))
    : scanning ? t("scan.active") : t("scan.stopped");

  el.scanText.textContent = scanLabel;
  el.statusScan.textContent = scanLabel;
  el.statusDot.className = "status-dot" + (enabled && scanning ? "" : state.hub.enableError ? " error" : " stopped");
  el.batteryChip.textContent = t("status.batteries", { n: batteries });
  el.statusSeen.textContent = t("status.devices", { n: seen });
  el.statusBatteries.textContent = t("status.batteries", { n: batteries });
  el.statusInterval.textContent = t("status.interval", { n: interval });
  el.refreshNote.textContent = t("grid.refresh", { n: interval });

  const pending = Number(state.hub.pending) || 0;
  const rejected = Number(state.hub.rejected) || 0;
  el.scanSummary.textContent = t("grid.progress", { pending, rejected });

  const samples = Number(state.session && state.session.samples) || 0;
  el.statusHistory.textContent = samples > 0
    ? t("status.samples", { n: samples })
    : t("status.samplesEmpty");
  el.statusHistory.title = state.config.dataDir || "";
  el.statusVersion.textContent = `v${state.config.version}`;
  el.statusVersion.title = `${state.config.platform} · ${state.config.dataDir || ""}`;

  const mqttSettings = (state.config.settings && state.config.settings.mqtt) || null;
  if (mqttSettings && mqttSettings.enabled) {
    el.statusMqtt.hidden = false;
    el.statusMqtt.textContent = t("status.mqtt", {
      state: state.config.mqttConnected ? t("status.mqttOn") : t("status.mqttOff"),
    });
  } else {
    el.statusMqtt.hidden = true;
  }

  if (lastUpdatedAgo == null) {
    el.statusUpdated.textContent = enabled ? t("status.waiting") : t("status.noBle");
  } else {
    el.statusUpdated.textContent = lastUpdatedAgo < 1.5
      ? t("status.justNow")
      : t("status.ago", { n: Math.round(lastUpdatedAgo) });
  }
}

async function poll() {
  try {
    const response = await fetch("/api/state", { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    state = await response.json();
    if (lastLanguage === "") {
      lastLanguage = initialLanguage(state.config.language);
      language = lastLanguage;
      applyStaticTranslations();
    }
    renderCards();
    renderChecking();
    renderDetail();
    renderChrome();
  } catch (error) {
    el.scanText.textContent = t("scan.noService");
    el.statusScan.textContent = t("scan.noService");
    el.statusDot.className = "status-dot error";
  }
}

el.deepscan.addEventListener("click", () => {
  fetch("/api/deepscan", { method: "POST" }).catch(() => {});
});

document.addEventListener("contextmenu", (event) => event.preventDefault());
document.addEventListener("dragstart", (event) => event.preventDefault());

applyStaticTranslations();
poll();
setInterval(poll, POLL_MS);
