// Simulation test for router/www/xray-panel-launcher.js (no browser needed).
// Run from the repo root:  node router/www/test/cardtest.js
// It loads the launcher into a vm sandbox with a fake router backend / fake DOM
// and prints what the stock SPA would see. Compare with the expected output in
// HANDOFF-DASHBOARD-INTEGRATION.md (Phase 5).
const fs=require('fs'), vm=require('vm');
const src=fs.readFileSync((process.argv[2] || require('path').join(__dirname,'..','xray-panel-launcher.js')),'utf8');
class El{ constructor(tag){this.tagName=tag;this.children=[];this.attrs={};this._text='';this.style={};this.listeners={};this.parentNode=null;this.className='';
   const self=this; this.classList={contains:c=>self.className.split(/\s+/).includes(c),toggle:(c,on)=>{const s=new Set(self.className.split(/\s+/).filter(Boolean)); on?s.add(c):s.delete(c); self.className=[...s].join(' ');},add:c=>self.classList.toggle(c,true),remove:c=>self.classList.toggle(c,false)}; this.dataset={}; }
  setAttribute(k,v){this.attrs[k]=String(v); if(k==='id') this.id=v;} getAttribute(k){return k in this.attrs?this.attrs[k]:null;} removeAttribute(k){delete this.attrs[k];} hasAttribute(k){return k in this.attrs;}
  appendChild(c){c.parentNode=this;this.children.push(c);return c;} insertBefore(c,ref){c.parentNode=this;const i=this.children.indexOf(ref);this.children.splice(i<0?this.children.length:i,0,c);return c;}
  removeChild(c){this.children=this.children.filter(x=>x!==c);c.parentNode=null;return c;}
  get firstChild(){return this.children[0]||null;}
  addEventListener(t,f){(this.listeners[t]=this.listeners[t]||[]).push(f);} click(){(this.listeners.click||[]).forEach(f=>f({}));}
  set textContent(v){this.children=[];this._text=String(v);} get textContent(){return this._text+this.children.map(c=>c.textContent).join('');}
  set innerHTML(v){this._html=v;} get innerHTML(){return this._html||'';}
  _all(){let o=[];for(const c of this.children){o.push(c);o=o.concat(c._all());}return o;}
  _match(sel){ let m; if((m=sel.match(/^\[([a-z0-9-]+)='([^']*)'\]$/))) return this.attrs[m[1]]===m[2]; if(sel[0]==='.') return sel.slice(1).split('.').every(c=>this.classList.contains(c)); if(sel[0]==='#') return this.id===sel.slice(1); return false; }
  querySelector(sel){ const last=sel.trim().split(/\s+/).pop(); return this._all().find(e=>e._match(last))||null; }
  querySelectorAll(sel){ const last=sel.trim().split(/\s+/).pop(); return this._all().filter(e=>e._match(last)); } }
const body=new El('body'), head=new El('head');
const wrap=new El('div'); wrap.className='vpn-dashboard-wrapper'; const cont=new El('div'); cont.className='tunnels-container';
const t1=new El('div'); t1.className='tunnel-card'; const add=new El('div'); add.className='add-tunnel-card'; cont.appendChild(t1); cont.appendChild(add); wrap.appendChild(cont); body.appendChild(wrap);
const noop=()=>{}; let dcl=null; const calls=[];
let state={service:{ok:true,value:false},tun:{ok:true,value:false},physical_switch:{ok:true,value:'on'},killswitch:{ok:true,value:false},bind_switch:{ok:true,value:false},enabled:{ok:true,value:true},active_profile:{ok:true,value:{id:'b',name:'NL-1',server:'nl.example.net',port:443}},native_vpn_active:{ok:true,value:true},sw_func:{ok:true,value:'vpn'}};
let profiles={active_id:'b',profiles:[{id:'a',name:'DE-1',server:'de.example.net',port:443,security:'reality',type:'tcp'},{id:'b',name:'NL-1',server:'nl.example.net',port:443,security:'reality',type:'tcp'},{id:'c',name:'US-1',server:'us.example.net',port:8443,security:'reality'}]};
const J=o=>Promise.resolve({ok:true,status:200,json:()=>Promise.resolve(JSON.parse(JSON.stringify(o))),text:()=>Promise.resolve('')});
const sandbox={console:{log:noop,warn:noop,error:(...a)=>console.log('ERR',...a)},setTimeout,clearTimeout,setInterval:()=>0,clearInterval:noop,Promise,JSON,Object,String,Array,Date,Math,RegExp,Error,isNaN,encodeURIComponent,
 location:{hash:'#/vpndashboard',protocol:'http:',hostname:'192.168.200.1'},
 document:{readyState:'loading',addEventListener:(t,f)=>{if(t==='DOMContentLoaded')dcl=f;},getElementById:id=>[head,body].flatMap(r=>r._all()).find(e=>e.id===id)||null,querySelector:s=>body.querySelector(s)||head.querySelector(s),querySelectorAll:s=>body.querySelectorAll(s),createElement:t=>new El(t),body,head,hidden:false,dispatchEvent:noop},
 fetch:(url,opts)=>{ const p=url.replace(/^https?:\/\/[^/]+/,''); calls.push((opts&&opts.method||'GET')+' '+p+(opts&&opts.body&&opts.body!=='{}'?' '+opts.body:''));
   if(p==='/api/launcher-config') return J({mode:'dashboard'}); if(p==='/api/state') return J(state); if(p==='/api/profiles') return J(profiles);
   if(p==='/api/live') return J({traffic:{ok:true,value:{up_total:1048576,down_total:5242880}},exit_ip:{ok:true,value:{ip:'203.0.113.7'}}});
   if(p==='/api/native-vpn/stop'){state.native_vpn_active.value=false;return J({});} if(p==='/api/native-vpn/restore') return J({});
   if(p==='/api/service'){const a=JSON.parse(opts.body).action; state.service.value=a==='start'; state.tun.value=a==='start'; return J({ok:true});}
   if(/\/activate$/.test(p)){const id=p.split('/')[3]; profiles.active_id=id; const pr=profiles.profiles.find(x=>x.id===id); state.active_profile.value={id,name:pr.name,server:pr.server,port:pr.port}; return J({switched:true});}
   if(/ipify/.test(url)) return Promise.resolve({ok:false}); return Promise.resolve({ok:false,status:404,json:()=>Promise.resolve(null),text:()=>Promise.resolve('')}); },
 XMLHttpRequest:function(){}, Image:function(){}, navigator:{}, Event:function(){}, AbortController:undefined };
sandbox.XMLHttpRequest.prototype={open:noop,send:noop};
sandbox.window=sandbox; sandbox.window.addEventListener=noop; sandbox.window.dispatchEvent=noop;
vm.createContext(sandbox); vm.runInContext(src,sandbox);
try{dcl&&dcl();}catch(e){console.log('init threw:',e.message);}
const wait=ms=>new Promise(r=>setTimeout(r,ms));
function dump(tag){ const c=cont.querySelector("[data-xray-card='1']"); if(!c){console.log(tag,'NO CARD');return null;}
  const g=k=>c.querySelector("[data-xr2='"+k+"']"); const rows=g('list').querySelectorAll('.xr2-row').map(r=>(r.classList.contains('is-active')?'*':' ')+r.querySelector('.xr2-row-name').textContent+' ['+r.querySelector('.xr2-row-sub').textContent+']');
  console.log(tag,'| pos',cont.children.indexOf(c),'of',cont.children.length,'| cls:',c.className,'| status:',g('status').textContent,'| hint:',g('hint').textContent,'| ks:',g('ks').className,'| sw display:',JSON.stringify(g('sw').style.display)); console.log('   profiles:',rows.join(' ; ')); console.log('   info:',g('info').children.map(r=>r.children.map(x=>x.textContent).join('=')).join(' ; ')); return c; }
(async()=>{ await wait(300); let c=dump('1 initial');
  c.querySelector("[data-xr2='power']").click(); await wait(2600); dump('2 after power ON'); await wait(2200); dump('2b settled');
  cont.querySelector("[data-xray-card='1']").querySelector("[data-xr2='list']").querySelectorAll('.xr2-row')[0].click(); await wait(400); dump('3 activated DE-1');
  state.bind_switch.value=true; state.sw_func.value='xray'; state.service.value=false; state.tun.value=false; state.physical_switch.value='off';
  sandbox.window.__noop; // force poll via power? use hashchange path: re-run activate module through MutationObserver absent → call fetch cycle by clicking nothing; emulate by removing card
  const cc=cont.querySelector("[data-xray-card='1']"); cont.removeChild(cc); 
  console.log('calls:',calls.filter(x=>!/state|live|launcher/.test(x)).join(' | '));
})();
