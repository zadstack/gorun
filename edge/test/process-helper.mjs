// Disposable test processes only. Never prints their potentially credential-bearing output.
import {spawn} from 'node:child_process';
export function terminate(child,signal='SIGKILL') {
 if(!child)return;
 try {process.platform==='win32'?child.kill(signal):process.kill(-child.pid,signal);}catch{}
}
export function command(args,{cwd,env,timeout=45000}={}) {
 return new Promise((resolve,reject)=>{
  const child=spawn(process.execPath,args,{cwd,env,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe']});
  let stdout='',bytes=0,settled=false;
  function finish(error,status) {
   if(settled)return;settled=true;clearTimeout(timer);terminate(child);
   child.stdout.destroy();child.stderr.destroy();child.unref();
   error?reject(error):resolve({status,stdout});
  }
  const timer=setTimeout(()=>finish(Error('Isolated test command deadline exceeded (output suppressed)')),timeout);
  child.stdout.on('data',v=>{bytes+=v.length;if(bytes>1048576)finish(Error('Isolated test output limit exceeded'));else stdout+=v;});
  child.stderr.on('data',v=>{bytes+=v.length;if(bytes>1048576)finish(Error('Isolated test output limit exceeded'));});
  child.on('error',()=>finish(Error('Isolated test command could not start')));
  child.on('close',status=>finish(null,status));
 });
}
