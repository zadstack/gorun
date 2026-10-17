// Maintainer-only extraction from the pinned local vendor. Output is committed;
// GoRun build/serve never requires Node, npm, a network request or this script.
import {readFileSync, writeFileSync, readdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
const root = fileURLToPath(new URL('../../', import.meta.url));
const vendorPath = root+'web/admin/static/vendor/lucide-1.55.0.js';
const source = readFileSync(vendorPath, 'utf8');
const sri = s => 'sha384-'+createHash('sha384').update(s).digest('base64');
const fullSRI = 'sha384-SZUFq9Dx112fyzY/iIBEjyd2gu3jY4XVhp+LUNXSpEMB2IEm/TOoMTNH4BHtQ6Hz';
if (sri(source) !== fullSRI) throw Error('Vendor changed: review the renderer extraction before updating.');
const full = createRequire(import.meta.url)(vendorPath);
const names = new Set();
const pascal = s => s.replace(/(?:^|[-_\s]+)(.)/g, (_, c) => c.toUpperCase());
function scan(dir) {
  for (const entry of readdirSync(dir, {withFileTypes:true})) {
    const path = dir+'/'+entry.name;
    if (entry.isDirectory() && !['vendor','testdata'].includes(entry.name)) scan(path);
    else if (entry.isFile() && /\.(go|html|js|json)$/.test(entry.name) && !/_test\./.test(entry.name)) {
      const content = readFileSync(path,'utf8');
      // Include every exact icon-name literal, also covering Go registry rows,
      // field-type icon choices and dynamically constructed core navigation.
      for (const match of content.matchAll(/["'`]([a-zA-Z][a-zA-Z0-9_-]*)["'`]/g)) {
        const name = pascal(match[1]);
        if (Object.hasOwn(full.icons,name)) names.add(name);
      }
    }
  }
}
scan(root+'internal');
scan(root+'web/admin');
const icons = Object.fromEntries([...names].sort().map(name => [name,full.icons[name]]));
const boundary = source.indexOf(',Ka=[[');
if (boundary < 0) throw Error('Pinned vendor renderer boundary missing');
const prefix = source.slice(0,boundary);
const lazy = readFileSync(root+'tools/admin-icons/lazy.js','utf8');
const generated = '// Generated from the pinned local Lucide vendor by tools/admin-icons/update.mjs.\n'+prefix+';const dS='+JSON.stringify(icons)+';const cS='+full.createIcons.toString()+';Object.assign(a,dS,{createElement:Ia,createIcons:cS,icons:dS});}));\n'+
  'if(typeof document!=="undefined"){\n'+lazy+'\n}\n';
const target = root+'web/admin/static/vendor/lucide-core-1.55.0.js';
const manifest = JSON.stringify({source:'vendor/lucide-1.55.0.js',sourceIntegrity:fullSRI,coreIntegrity:sri(generated),icons:Object.keys(icons)},null,2)+'\n';
if (process.argv.includes('--check')) {
  if (readFileSync(target,'utf8') !== generated || readFileSync(root+'tools/admin-icons/manifest.json','utf8') !== manifest) throw Error('Run node tools/admin-icons/update.mjs and review the new icon subset.');
} else {
  writeFileSync(target,generated);
  writeFileSync(root+'tools/admin-icons/manifest.json',manifest);
  const head = root+'web/admin/templates/base.html';
  const html = readFileSync(head,'utf8');
  const script = `<script defer src="{{asset "vendor/lucide-core-1.55.0.js"}}" integrity="${sri(generated)}" crossorigin="anonymous" data-lucide-full="{{asset "vendor/lucide-1.55.0.js"}}" data-lucide-integrity="${fullSRI}"></script>`;
  const provenancePath = root+'web/admin/static/cdn-provenance.json';
  const provenance = JSON.parse(readFileSync(provenancePath,'utf8'));
  const original = provenance.assets.find(a => a.local_path === 'web/admin/static/vendor/lucide-1.55.0.js');
  const record = {...original, package:'lucide-core', local_path:'web/admin/static/vendor/lucide-core-1.55.0.js', integrity:sri(generated), bytes:Buffer.byteLength(generated), derivation:'Selected icon data and unchanged renderer from pinned local vendor; tools/admin-icons/update.mjs', source_integrity:fullSRI};
  record.source_url = record.url;
  delete record.url;
  delete record.current_alias_matches;
  provenance.assets = [...provenance.assets.filter(a => a.package !== 'lucide-core'),record];
  writeFileSync(provenancePath,JSON.stringify(provenance,null,2)+'\n');
  writeFileSync(head,html.replace(/<script defer src="{{asset "vendor\/lucide(?:-core)?-1\.55\.0\.js"}}"[^>]*><\/script>/,script));
}
console.log(JSON.stringify({icons:names.size,fullBytes:Buffer.byteLength(source),coreBytes:Buffer.byteLength(generated),coreIntegrity:sri(generated)}));
