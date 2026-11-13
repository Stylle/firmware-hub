// Local-only Docker preview. Generated credentials never enter Git or arguments.
import {randomBytes} from 'node:crypto';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const local=path.join(root,'.local');await mkdir(local,{recursive:true});
let credentials;try{credentials=JSON.parse(await readFile(path.join(local,'credentials.json'),'utf8'))}catch(e){if(e.code!=='ENOENT')throw e;credentials={name:'admin',password:randomBytes(24).toString('base64url')};await writeFile(path.join(local,'credentials.json'),JSON.stringify(credentials,null,2),{mode:0o600})}
const compose={services:{'firmware-hub':{image:'firmware-hub:local',container_name:'firmware-hub-local',restart:'unless-stopped',environment:{USER:credentials.name,PASSWORD:credentials.password},ports:['127.0.0.1:28183:8080'],volumes:['firmware-hub-local-data:/data']}},volumes:{'firmware-hub-local-data':{}}};
await writeFile(path.join(local,'compose.json'),JSON.stringify(compose,null,2),{mode:0o600});
const distro=process.env.FH_WSL_DISTRO;
const run=(args)=>{const r=spawnSync('wsl.exe',[...(distro?['-d',distro]:[]),'-u','root','--',...args],{cwd:root,stdio:'inherit'});if(r.status!==0)process.exit(r.status||1)};
run(['docker','build','-t','firmware-hub:local','.']);run(['docker','compose','-f','.local/compose.json','up','-d']);
console.log('Local Docker preview: http://127.0.0.1:28183');console.log('Administrator credentials: .local/credentials.json (local only; never publish).');
