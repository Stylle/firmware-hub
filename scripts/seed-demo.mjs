// Explicitly creates an isolated demonstration project via normal authenticated
// APIs. Credentials and local-only publisher keys stay in .local, never Git.
import {readFile,writeFile} from 'node:fs/promises';
import {generateKeyPairSync,sign,createHash} from 'node:crypto';
const base=process.env.FH_URL||'http://127.0.0.1:28183';
const credentials=JSON.parse(await readFile('.local/credentials.json','utf8'));
const login=await fetch(base+'/api/v1/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(credentials)});if(!login.ok)throw Error('Demo login failed: '+login.status);const session=await login.json(),cookie=login.headers.get('set-cookie').split(';')[0];
const api=async(path,method='GET',body)=>{const headers={Cookie:cookie,'X-CSRF-Token':session.user.csrf};if(!(body instanceof FormData))headers['Content-Type']='application/json';const r=await fetch(base+'/api/v1'+path,{method,headers,body:body instanceof FormData?body:body?JSON.stringify(body):undefined});const data=await r.json();if(!r.ok)throw Error(data.message||r.status);return data};
const slug='demo-controller';const existing=(await api('/projects')).find(p=>p.slug===slug);
if(existing){console.log('Demo project already exists; retained existing data.');process.exit(0)}
const {publicKey,privateKey}=generateKeyPairSync('ed25519');const pub=publicKey.export({type:'spki',format:'der'}).subarray(-32).toString('hex');
await writeFile('.local/demo-publisher.pem',privateKey.export({type:'pkcs8',format:'pem'}),{mode:0o600});
const project=await api('/projects','POST',{slug,name:'示例设备控制器',description:'用于本地验证发布、验签下载和可选设备心跳的演示项目。',public:false,telemetry:true,heartbeat:15,timeout:30,public_key:pub});
const device=await api(`/projects/${project.id}/devices`,'POST',{name:'Qt 测试终端'});
const data=Buffer.alloc(256*1024);for(let i=0;i<data.length;i++)data[i]=i%251;
const payload=Buffer.from(['FH1',slug,'1.1.0','2','1','stable','test','x64','demo',createHash('sha256').update(data).digest('hex'),String(data.length),'0',String(Math.floor(Date.now()/1000)+90*86400),''].join('\n'));
const envelope={payload:payload.toString('base64'),signature:sign(null,payload,privateKey).toString('hex')};
await writeFile('.local/demo-firmware.bin',data);await writeFile('.local/demo-manifest.json',JSON.stringify(envelope,null,2));
const form=new FormData();form.append('manifest',JSON.stringify(envelope));form.append('notes','演示版本 1.1.0\n\n• 独立 Ed25519 签名与 SHA-256 校验\n• 支持本地 Qt 客户端下载验证\n• 状态上报由客户端、项目和 Hub 三方开关共同控制\n\n此文件仅供传输测试，不可刷写到真实硬件。');form.append('file',new Blob([data]),'demo-firmware.bin');const release=await api(`/projects/${project.id}/releases`,'POST',form);await api(`/releases/${release.id}/publish`,'PUT',{published:true});
await writeFile('.local/demo.json',JSON.stringify({url:base,project:slug,project_id:project.id,public_key:pub,token:device.token,release_id:release.id,sha256:createHash('sha256').update(data).digest('hex')},null,2),{mode:0o600});
console.log('Demo project, signed firmware and independent Qt device created. Connection data saved in .local/demo.json.');
