(()=>{
var n=navigator,l=location,d=document,h=history,r=d.referrer,last;
if(n.doNotTrack=="1"||n.globalPrivacyControl===true)return;
function s(){
var q=new URLSearchParams(l.search),u=new URLSearchParams();
for(var k of["utm_source","utm_medium","utm_campaign"])if(q.has(k))u.set(k,q.get(k));
var t=String(u),p=l.pathname+(t?"?"+t:"");
if(p===last)return;
last=p;
var b=JSON.stringify({p:p,r:r,w:innerWidth,l:d.documentElement.lang});
r="";
try{if(n.sendBeacon&&n.sendBeacon("/_a/e",b))return}catch(e){}
fetch("/_a/e",{method:"POST",body:b,keepalive:true}).catch(()=>{})
}
var o=h.pushState;
h.pushState=function(){var x=o.apply(this,arguments);s();return x};
addEventListener("popstate",s);
s()
})();
