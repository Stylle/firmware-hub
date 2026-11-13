// Local-only end-to-end fixture: account publication -> real native Qt download
// -> independent desktop installer -> DLL replacement -> rollback.
// Uses only newly created temporary application directories under .local.
import { readFile, writeFile, mkdir, mkdtemp } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import path from "node:path";
import assert from "node:assert/strict";
const local = path.resolve(".local");
await mkdir(local, { recursive: true });
const base = process.env.FH_URL || "http://127.0.0.1:28183";
const credentials = JSON.parse(
  await readFile(path.join(local, "credentials.json"), "utf8"),
);
let response = await fetch(base + "/api/v1/session", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(credentials),
});
assert.equal(response.status, 200);
const cookie = response.headers.get("set-cookie").split(";")[0],
  csrf = (await response.json()).user.csrf;
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
  const data = await r.json();
  assert.ok(r.ok, JSON.stringify(data));
  return data;
}
let project = (await api("/projects")).find((p) => p.slug === "desktop-demo");
if (!project)
  project = await api("/projects", "POST", {
    slug: "desktop-demo",
    name: "桌面软件更新示例",
    description: "账号发布模式：整包与单文件更新的本地集成示例。",
    public: true,
    telemetry: false,
    heartbeat: 60,
    timeout: 180,
    public_key: "",
  });
assert.equal(project.public_key, "");
const history = await api(`/projects/${project.id}/releases?limit=1`),
  next = (history[0]?.sequence || 0) + 1;
const appDir = await mkdtemp(path.join(local, "desktop-app-"));
await writeFile(
  path.join(appDir, "settings.ini"),
  "user settings must survive",
);
await writeFile(path.join(appDir, "module.dll"), "old library fixture");
const full = await readFile(
  process.env.FH_DESKTOP_ZIP || path.join(local, "desktop-full.zip"),
);
const dll = await readFile(
  process.env.FH_DESKTOP_DLL || "build/qt-demo/Qt5SerialPort.dll",
);
const qt = path.resolve(
  process.env.FH_QT_BINARY || "build/qt-demo/firmware-hub-demo.exe",
);
const installer = path.resolve(
  process.env.FH_UPDATER_BINARY || "build/qt-demo/firmware-hub-update.exe",
);
const installed = [];
for (const [index, bytes] of [full, dll].entries()) {
  const version = `0.2.${next + index}`,
    kind = index ? "file" : "full",
    filename = index ? "module.dll" : "desktop.zip";
  const metadata = {
    version,
    package_kind: kind,
    channel: "stable",
    platform: "any",
    arch: "any",
    hardware: "any",
    base_version: index ? installed[0] : "",
    target_path: index ? "module.dll" : "",
  };
  const form = new FormData();
  form.append("metadata", JSON.stringify(metadata));
  form.append(
    "notes",
    index
      ? "仅替换 module.dll；不改动用户配置。"
      : "Qt 示例 EXE 整包覆盖测试。",
  );
  form.append("file", new Blob([bytes]), filename);
  response = await fetch(base + `/api/v1/projects/${project.id}/releases`, {
    method: "POST",
    headers: { Cookie: cookie, "X-CSRF-Token": csrf },
    body: form,
  });
  const result = await response.json();
  assert.equal(response.status, 201, JSON.stringify(result));
  await api(`/releases/${result.id}/publish`, "PUT", { published: true });
  const output = path.join(
      appDir,
      `../download-${path.basename(appDir)}-${index}.bin`,
    ),
    plan = output + ".install.json";
  const env = {
    ...process.env,
    QT_QPA_PLATFORM: "offscreen",
    FH_URL: base,
    FH_PROJECT: project.slug,
    FH_PUBLIC_KEY: "",
    FH_TOKEN: "",
    FH_OUTPUT: output,
    FH_PLAN_DIR: appDir,
    FH_PLAN_FILE: plan,
    FH_CURRENT_VERSION: index ? installed[0] : "0.0.0",
    FH_PLATFORM: "any",
    FH_ARCH: "any",
    FH_HARDWARE: "any",
    FH_TELEMETRY: "0",
    FH_STATE_DIR: path.join(appDir, "../state-" + path.basename(appDir)),
  };
  let r = spawnSync(qt, ["--smoke"], { env, encoding: "utf8", timeout: 70000 });
  assert.equal(r.status, 0, r.stdout + r.stderr);
  assert.match(r.stdout, /LOCAL_INSTALL_PLAN_READY/);
  assert.equal(
    createHash("sha256")
      .update(await readFile(output))
      .digest("hex"),
    createHash("sha256").update(bytes).digest("hex"),
  );
  r = spawnSync(installer, ["--plan", plan], {
    encoding: "utf8",
    timeout: 30000,
  });
  assert.equal(r.status, 0, r.stdout + r.stderr);
  const state = JSON.parse(
    await readFile(path.join(appDir, ".firmware-hub-version.json"), "utf8"),
  );
  assert.equal(state.version, version);
  assert.equal(
    await readFile(path.join(appDir, "settings.ini"), "utf8"),
    "user settings must survive",
  );
  installed.push(version);
  console.log(
    `PASS account publish -> native Qt -> ${kind === "full" ? "ZIP installation" : "DLL replacement"} -> installed version ledger`,
  );
}
assert.equal(
  createHash("sha256")
    .update(await readFile(path.join(appDir, "module.dll")))
    .digest("hex"),
  createHash("sha256").update(dll).digest("hex"),
);
let rollback = spawnSync(installer, ["--rollback", appDir], {
  encoding: "utf8",
  timeout: 30000,
});
assert.equal(rollback.status, 0, rollback.stdout + rollback.stderr);
assert.equal(
  await readFile(path.join(appDir, "module.dll"), "utf8"),
  "old library fixture",
);
assert.equal(
  JSON.parse(
    await readFile(path.join(appDir, ".firmware-hub-version.json"), "utf8"),
  ).version,
  installed[0],
);
console.log(
  "PASS rollback restores old DLL and installed version without changing user settings",
);
response = await fetch(base + "/api/v1/public");
assert.equal(response.status, 200);
assert.ok((await response.json()).some((p) => p.slug === project.slug));
response = await fetch(base + "/api/v1/public/" + project.slug);
assert.equal(response.status, 200);
const published = await response.json();
assert.ok(published.releases.every((r) => r.published_by === credentials.name));
console.log(
  "PASS anonymous public discovery/history and server-recorded publisher attribution",
);
await api("/session", "DELETE");
console.log("Local application fixture retained:", appDir);
