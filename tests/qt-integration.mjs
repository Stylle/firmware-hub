// Test-only fault-injection proxy, never used by production or browser clients.
import http from 'node:http';
import https from 'node:https';
import {spawn} from 'node:child_process';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {once} from 'node:events';
import assert from 'node:assert/strict';
import path from 'node:path';
const d=JSON.parse(await readFile('.local/demo.json','utf8'));
const credentials=JSON.parse(await readFile('.local/credentials.json','utf8'));
let mode='normal',cut=false,ranges=0,reports=0,artifactRequests=0;
const handler=async(req,res)=>{try{let body;for await(const b of req)body=body?Buffer.concat([body,b]):b;if(req.url.includes('/heartbeat'))reports++;if(req.url.includes('/artifacts/'))artifactRequests++;
 const headers={};for(const k of ['authorization','content-type','range','if-range'])if(req.headers[k])headers[k]=req.headers[k];
 const upstream=await fetch(d.url+req.url,{method:req.method,headers,body});const data=Buffer.from(await upstream.arrayBuffer());const outHeaders=Object.fromEntries(upstream.headers);delete outHeaders['transfer-encoding'];delete outHeaders['content-encoding'];delete outHeaders['connection'];
 if(req.url.includes('/check')&&mode==='bad-signature'){const obj=JSON.parse(data);obj.release.signature=(obj.release.signature[0]==='0'?'1':'0')+obj.release.signature.slice(1);const b=Buffer.from(JSON.stringify(obj));outHeaders['content-length']=b.length;res.writeHead(upstream.status,outHeaders);res.end(b);return}
 if(req.url.includes('/artifacts/')){
  if(req.headers.range)ranges++;
  if(mode==='resume'&&!cut){cut=true;res.writeHead(upstream.status,outHeaders);res.write(data.subarray(0,65536));setTimeout(()=>res.destroy(),30);return}
  if(mode==='bad-etag')outHeaders.etag='"untrusted-other-version"';
  if(mode==='bad-hash'&&data.length)data[0]^=1;
 }
 res.writeHead(upstream.status,outHeaders);res.end(data);
 }catch{if(!res.headersSent)res.writeHead(502);res.end()}};
const server=http.createServer(handler);server.listen(0,'127.0.0.1');await once(server,'listening');
const tls=https.createServer({key:await readFile('.local/tls/server.key'),cert:await readFile('.local/tls/server.pem')},handler);tls.listen(0,'127.0.0.1');await once(tls,'listening');
const url=`http://127.0.0.1:${server.address().port}`,tlsURL=`https://127.0.0.1:${tls.address().port}`;
const run=async(consent='0',base=url,ca='')=>{const child=spawn(process.execPath,['scripts/test-qt.mjs'],{env:{...process.env,FH_URL:base,FH_TELEMETRY:consent,FH_CA_FILE:ca},stdio:['ignore','pipe','pipe']});let output='';child.stdout.on('data',b=>output+=b);child.stderr.on('data',b=>output+=b);const [code]=await once(child,'close');return {code,output}};
const session=await fetch(d.url+'/api/v1/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(credentials)});assert.equal(session.status,200);const cookie=session.headers.get('set-cookie').split(';')[0],csrf=(await session.json()).user.csrf;
const api=async(p,method='GET',body)=>{const r=await fetch(d.url+'/api/v1'+p,{method,headers:{Cookie:cookie,'X-CSRF-Token':csrf,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});assert.equal(r.status,200);return r.json()};const settings=await api('/settings');
try{
 mode='resume';let result=await run();assert.equal(result.code,0,result.output);assert.ok(ranges>0,'Qt did not resume with Range');assert.equal(createHash('sha256').update(await readFile('.local/qt-download.bin')).digest('hex'),d.sha256);console.log('PASS Qt actual HTTP interrupted download + Range resume + SHA-256');
 for(const failure of ['bad-signature','bad-etag','bad-hash']){mode=failure;const before=artifactRequests;result=await run();assert.notEqual(result.code,0,'unsafe artifact accepted: '+failure);if(failure==='bad-signature')assert.equal(artifactRequests,before,'download started before signature validation');console.log('PASS Qt refuses '+failure)}
 mode='normal';reports=0;result=await run('0');assert.equal(result.code,0,result.output);assert.equal(reports,0);console.log('PASS Qt local telemetry off sends zero heartbeat requests');
 await api('/settings','PUT',{telemetry:false});reports=0;result=await run('1');assert.equal(result.code,0,result.output);assert.equal(reports,0);console.log('PASS Hub telemetry off stops client collection without blocking downloads');
 await api('/settings','PUT',{telemetry:true});reports=0;result=await run('1');assert.equal(result.code,0,result.output);assert.ok(reports>0);console.log('PASS explicit bilateral opt-in sends heartbeat');
 result=await run('0',tlsURL,path.resolve('.local/tls/ca.pem'));assert.equal(result.code,0,result.output);console.log('PASS Qt actual HTTPS with explicitly trusted local CA');
 result=await run('0',tlsURL,'');assert.notEqual(result.code,0,'untrusted TLS accepted');console.log('PASS Qt rejects untrusted HTTPS certificate');
}finally{await api('/settings','PUT',settings);server.closeAllConnections();tls.closeAllConnections();await Promise.all([new Promise(r=>server.close(r)),new Promise(r=>tls.close(r))])}
