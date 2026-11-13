// Runs an already compiled Qt example against the isolated Docker demo.
// Qt/MinGW DLL locations must be on PATH; no credentials are printed.
import {readFile,mkdir} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
import path from 'node:path';
const d=JSON.parse(await readFile('.local/demo.json','utf8'));
await mkdir('.local/qt-state',{recursive:true});
const env={...process.env,FH_URL:process.env.FH_URL||d.url,FH_PROJECT:d.project,FH_PUBLIC_KEY:d.public_key,FH_TOKEN:d.token,FH_OUTPUT:path.resolve('.local/qt-download.bin'),FH_STATE_DIR:path.resolve('.local/qt-state'),FH_TELEMETRY:process.env.FH_TELEMETRY||'1',QT_QPA_PLATFORM:'offscreen'};
const binary=path.resolve(process.argv[2]||process.env.FH_QT_BINARY||'build/qt/release/firmware-hub-demo.exe');
const r=spawnSync(binary,['--smoke'],{env,encoding:'utf8',timeout:70000});
if(r.stdout)process.stdout.write(r.stdout);if(r.stderr)process.stderr.write(r.stderr);if(r.error)console.error(r.error.message);process.exit(r.status??1);
