// Tests the compiled NEW reference application through Hub and real native Qt.
// Does not open a serial port, reset a board, or write physical flash.
import { readFile, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import path from "node:path";
import assert from "node:assert/strict";
const firmware = process.env.FH_N32_IMAGE;
if (!firmware) throw Error("Set FH_N32_IMAGE to the newly compiled app-v2.bin");
const bytes = await readFile(firmware),
  hash = createHash("sha256").update(bytes).digest("hex");
assert.equal(bytes.readUInt32LE(0x400), 0x46484150);
assert.equal(bytes.subarray(0x404, 0x445).toString().split("\0")[0], "1.1.0");
const base = process.env.FH_URL || "http://127.0.0.1:28183",
  credentials = JSON.parse(await readFile(".local/credentials.json", "utf8"));
const login = await fetch(base + "/api/v1/session", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(credentials),
});
assert.equal(login.status, 200);
const cookie = login.headers.get("set-cookie").split(";")[0],
  csrf = (await login.json()).user.csrf;
async function api(p, method = "GET", body) {
  const r = await fetch(base + "/api/v1" + p, {
    method,
    headers: {
      Cookie: cookie,
      "X-CSRF-Token": csrf,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const v = await r.json();
  assert.ok(r.ok, JSON.stringify(v));
  return v;
}
let project = (await api("/projects")).find((p) => p.slug === "n32-minimal");
if (!project)
  project = await api("/projects", "POST", {
    slug: "n32-minimal",
    name: "最小 MCU 升级示例",
    description:
      "新写的 N32G452 示例；需先烧录新 Bootloader，尚未完成真实板卡刷写验证。",
    public: false,
    telemetry: false,
    heartbeat: 60,
    timeout: 180,
    public_key: "",
  });
let release = (await api(`/projects/${project.id}/releases`)).find(
  (r) => r.version === "1.1.0",
);
if (!release) {
  const form = new FormData();
  form.append(
    "metadata",
    JSON.stringify({
      version: "1.1.0",
      channel: "stable",
      platform: "mcu",
      arch: "cortex-m4",
      hardware: "n32g452-demo",
      package_kind: "full",
    }),
  );
  form.append(
    "notes",
    "## 全新最小参考程序\n- 版本 1.1.0\n- 仅适用已首次烧录本项目 Bootloader 的参考设备\n\n**尚未完成真实设备刷写验证。不可直接发送给未知原有 Bootloader。**",
  );
  form.append("file", new Blob([bytes]), "app-v2.bin");
  const r = await fetch(base + `/api/v1/projects/${project.id}/releases`, {
    method: "POST",
    headers: { Cookie: cookie, "X-CSRF-Token": csrf },
    body: form,
  });
  release = await r.json();
  assert.equal(r.status, 201);
  await api(`/releases/${release.id}/publish`, "PUT", { published: true });
} else {
  assert.equal(
    release.sha256,
    hash,
    "immutable version differs: publish a new version instead of overwriting",
  );
}
let config;
try {
  config = JSON.parse(await readFile(".local/n32-demo.json", "utf8"));
} catch (e) {
  if (e.code !== "ENOENT") throw e;
  const credential = await api(`/projects/${project.id}/devices`, "POST", {
    name: "最小示例接入（尚未上报）",
  });
  config = {
    url: base,
    project: project.slug,
    public_key: "",
    token: credential.token,
    version: "1.0.0",
    sequence: 0,
    security: 0,
    platform: "mcu",
    arch: "cortex-m4",
    hardware: "n32g452-demo",
  };
  await writeFile(".local/n32-demo.json", JSON.stringify(config, null, 2), {
    mode: 0o600,
  });
}
const output = path.resolve(".local/n32-downloaded.bin");
const r = spawnSync(
  path.resolve(
    process.env.FH_QT_BINARY || "build/qt-demo/firmware-hub-demo.exe",
  ),
  ["--smoke"],
  {
    env: {
      ...process.env,
      QT_QPA_PLATFORM: "offscreen",
      FH_URL: base,
      FH_PROJECT: config.project,
      FH_PUBLIC_KEY: "",
      FH_TOKEN: config.token,
      FH_OUTPUT: output,
      FH_CURRENT_VERSION: config.version,
      FH_PLATFORM: config.platform,
      FH_ARCH: config.arch,
      FH_HARDWARE: config.hardware,
      FH_TELEMETRY: "0",
      FH_STATE_DIR: path.resolve(".local/n32-qt-state"),
    },
    encoding: "utf8",
    timeout: 70000,
  },
);
assert.equal(r.status, 0, r.stdout + r.stderr);
assert.equal(
  createHash("sha256")
    .update(await readFile(output))
    .digest("hex"),
  hash,
);
await api("/session", "DELETE");
console.log(
  "PASS newly compiled MCU app -> account publication -> native Qt compatibility check/download -> exact SHA-256. No serial/flash operation.",
);
