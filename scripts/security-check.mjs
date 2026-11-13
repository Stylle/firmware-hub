// Produces a local scanner report without exposing credentials or user data.
import {spawnSync} from 'node:child_process';
import {mkdir,writeFile} from 'node:fs/promises';
const go=process.env.GO_BIN||'go';
const result=spawnSync(go,['run','golang.org/x/vuln/cmd/govulncheck@v1.9.0','-format=json','./...'],{encoding:'utf8',maxBuffer:16*1024*1024,timeout:300000});
await mkdir('.local',{recursive:true});await writeFile('.local/govulncheck.json',result.stdout||'',{mode:0o600});
if(result.stderr)process.stderr.write(result.stderr);if(result.error)console.error(result.error.message);
console.log('govulncheck exit:',result.status,'; detailed local report: .local/govulncheck.json');process.exit(result.status??1);
