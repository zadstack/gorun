// Embed a reproducible, locally compiled Worker in GoRun Full. No provider calls.
import {readFile,readdir,mkdir,writeFile} from 'node:fs/promises';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
const edge=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const target=resolve(edge,'../internal/edgeprovision/runtime');
const files=[];
for(const name of ['admin-auth.js','admin.js','index.js'])files.push([name,await readFile(resolve(edge,'dist',name))]);
for(const name of (await readdir(resolve(edge,'migrations'))).filter(x=>/^\d+.*\.sql$/.test(x)).sort())files.push([name,await readFile(resolve(edge,'migrations',name))]);
files.sort(([a],[b])=>a.localeCompare(b,'en'));
const hash=createHash('sha256');for(const [name,bytes]of files){hash.update(name+'\0');hash.update(bytes);}
files.push(['manifest.json',Buffer.from(JSON.stringify({sha256:hash.digest('hex'),compatibility_date:'2026-10-01',main_module:'index.js',files:files.map(([name])=>name)},null,2)+'\n')]);
const check=process.argv.includes('--check');await mkdir(target,{recursive:true});
for(const [name,bytes]of files){const path=resolve(target,name);if(check){if(!(await readFile(path)).equals(bytes))throw Error('Regenerate manager runtime: '+name);}else await writeFile(path,bytes);}
console.log(check?'Embedded Edge runtime matches compiled sources.':'Embedded Edge runtime generated.');
