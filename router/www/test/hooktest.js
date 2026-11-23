// Simulation test for router/www/xray-panel-launcher.js (no browser needed).
// Run from the repo root:  node router/www/test/hooktest.js
// It loads the launcher into a vm sandbox with a fake router backend / fake DOM
// and prints what the stock SPA would see. Compare with the expected output in
// HANDOFF-DASHBOARD-INTEGRATION.md (Phase 5).
const fs=require('fs'), vm=require('vm');
const src=fs.readFileSync((process.argv[2] || require('path').join(__dirname,'..','xray-panel-launcher.js')),'utf8');
let uci={func:'vpn',sub_func:'5003'}; const sideCalls=[]; const stockCalls=[];
// fake router RPC backend
function backend(body){ const r=JSON.parse(body); const [sid,mod,fn,arg]=r.params; stockCalls.push(mod+'.'+fn+(fn==='set_config'?JSON.stringify(arg):''));
  let result={};
  if(mod==='switch-button'&&fn==='get_funcs') result={funcs:[{func:'adguardhome'},{func:'led'},{func:'wifi',sub_func:[{func:'main_wifi'}]},{func:'vpn',sub_func:[{func:'5003'}]}]};
  if(mod==='switch-button'&&fn==='get_config') result={...uci};
  if(mod==='switch-button'&&fn==='set_config'){ uci={func:arg.func,sub_func:arg.sub_func}; result={}; }
  if(mod==='switch-button'&&fn==='check_sync_status') result={sync_status:1};
  if(mod==='vpn-client'&&fn==='get_tunnel') result={tunnels:[{tunnel_id:5003,name:'Tunnel 1',via:{type:'wireguard'}}]};
  return JSON.stringify({id:r.id,jsonrpc:'2.0',result}); }
class FakeXHR{ constructor(){this.readyState=0;this._l=[];this.responseType='';this._text='';}
  open(m,u){this.readyState=1;} addEventListener(t,f){ if(t==='readystatechange') this._l.push(f);} 
  get responseText(){return this._text;} get response(){return this._text;}
  send(b){ setTimeout(()=>{ this._text=backend(b); this.readyState=4; this._l.forEach(f=>f()); if(this.onreadystatechange) this.onreadystatechange(); },5); } }
const noop=()=>{}; let dcl=null; let sideFail=false;
const el=()=>({style:{},classList:{add:noop,remove:noop,contains:()=>false},appendChild:noop,setAttribute:noop,querySelector:()=>null,querySelectorAll:()=>[],addEventListener:noop});
const sandbox={ console:{log:noop,warn:noop,error:noop}, setTimeout, clearTimeout, setInterval:()=>0, clearInterval:noop, Promise, JSON, Object, String, Array, Date, Math, RegExp, Error,
  location:{hash:'#/btnsettings',protocol:'http:',hostname:'192.168.200.1'},
  document:{readyState:'loading',addEventListener:(t,f)=>{if(t==='DOMContentLoaded')dcl=f;},getElementById:()=>null,querySelector:()=>null,querySelectorAll:()=>[],createElement:el,body:el(),head:el(),hidden:false},
  fetch:(url,opts)=>{ if(url.endsWith('/api/launcher-config')) return Promise.resolve({ok:true,json:()=>Promise.resolve({mode:'dashboard'})});
     if(url.endsWith("/api/service")){ sideCalls.push("service:"+JSON.parse(opts.body).action); return Promise.resolve({ok:true,json:()=>Promise.resolve({})}); } if(url.endsWith("/api/side-switch")){ const on=JSON.parse(opts.body).on; sideCalls.push(on); if(sideFail) return Promise.resolve({ok:false,status:401,text:()=>Promise.resolve('')}); uci.func= on?'xray':'vpn'; return Promise.resolve({ok:true,json:()=>Promise.resolve({})}); }
     return Promise.resolve({ok:false,status:404,json:()=>Promise.resolve(null),text:()=>Promise.resolve('')}); },
  XMLHttpRequest:FakeXHR, Image:function(){}, navigator:{} };
sandbox.window=sandbox; sandbox.window.addEventListener=noop;
vm.createContext(sandbox); vm.runInContext(src,sandbox);
try{ dcl&&dcl(); }catch(e){ console.log('init threw (ignored):',e.message); }
function rpc(mod,fn,arg){ return new Promise(res=>{ const x=new sandbox.XMLHttpRequest(); x.open('POST','/rpc'); x.onreadystatechange=()=>{ if(x.readyState===4) res(JSON.parse(x.responseText).result); }; x.send(JSON.stringify({jsonrpc:'2.0',id:1,method:'call',params:['sid',mod,fn,arg||{}]})); }); }
(async()=>{
  console.log('funcs  :',JSON.stringify((await rpc('switch-button','get_funcs')).funcs.find(f=>f.func==='vpn')));
  console.log('tunnels:',JSON.stringify((await rpc('vpn-client','get_tunnel')).tunnels.map(t=>[t.tunnel_id,t.name])));
  console.log('config (uci vpn/5003):',JSON.stringify(await rpc('switch-button','get_config')));
  console.log('check xray:',JSON.stringify(await rpc('switch-button','check_sync_status',{func:'vpn',sub_func:'xray'})));
  console.log('set → xray:',JSON.stringify(await rpc('switch-button','set_config',{func:'vpn',sub_func:'xray',sync:false})),'uci=',JSON.stringify(uci));
  console.log('config (uci xray):',JSON.stringify(await rpc('switch-button','get_config')));
  console.log('set → tunnel:',JSON.stringify(await rpc('switch-button','set_config',{func:'vpn',sub_func:'5003',sync:false})),'uci=',JSON.stringify(uci));
  console.log('config after:',JSON.stringify(await rpc('switch-button','get_config')));
  sideFail=true; console.log('set → xray (panel 401):',JSON.stringify(await rpc('switch-button','set_config',{func:'vpn',sub_func:'xray'})),'uci=',JSON.stringify(uci));
  sandbox.location.hash='#/vpndashboard'; console.log('tunnels on dashboard:',JSON.stringify((await rpc('vpn-client','get_tunnel')).tunnels.map(t=>[t.tunnel_id,t.name])));
  console.log('side-switch calls:',JSON.stringify(sideCalls)); console.log('stock saw:',stockCalls.join(' | '));
})();
