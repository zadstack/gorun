#!/usr/bin/env python3
"""Controlled synthetic comparison; requires a disposable Linux Docker host."""
import argparse
import base64
import hashlib
import http.client
import json
import os
from pathlib import Path
import platform
import secrets
import shutil
import subprocess
import threading
import time
import urllib.request

args = argparse.ArgumentParser()
args.add_argument('--output', type=Path, required=True)
out = args.parse_args().output.resolve()
out.mkdir(parents=True, exist_ok=True)
fixture = out / 'fixture'
fixture.mkdir(exist_ok=True)
cpu = sorted(os.sched_getaffinity(0))
if len(cpu) < 4:
    raise SystemExit('At least four logical CPUs are required; no measurements generated.')
server_cpus = ','.join(map(str, cpu[:2]))
load_cpus = ','.join(map(str, cpu[2:]))
images = {'wordpress': 'wordpress:7.1.3-php8.3-apache', 'database': 'mariadb:11.4',
          'wpcli': 'wordpress:cli-php8.3', 'node': 'node:24-alpine', 'nginx': 'nginx:stable-alpine'}
containers = []
volumes = []
network = 'gorun-bench-' + secrets.token_hex(4)

def run(*cmd, input=None):
    return subprocess.check_output(cmd, input=input, stderr=subprocess.PIPE).decode()

def docker(*cmd, input=None):
    return run('docker', *cmd, input=input)

def write(name, data):
    (out / name).write_text(json.dumps(data, indent=2) + '\n')

posts = [{'title': f'Benchmark article {i:04}', 'slug': f'post-{i:04}',
          'body': '<p>GORUN_COMPARISON_BODY ' + ('Synthetic article content. ' * 160) + f'Article {i:04}.</p>'}
         for i in range(1, 101)]
(fixture / 'posts.json').write_text(json.dumps(posts))
metadata = {'status': 'incomplete', 'edge': {'status': 'unavailable', 'reason': 'No runnable Edge CMS'},
            'commit': run('git', 'rev-parse', 'HEAD').strip(), 'utc': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
            'platform': platform.platform(), 'cpus': cpu, 'server_cpus': server_cpus, 'load_cpus': load_cpus,
            'cpu_info': Path('/proc/cpuinfo').read_text(), 'mem_info': Path('/proc/meminfo').read_text(),
            'fixture_sha256': hashlib.sha256((fixture/'posts.json').read_bytes()).hexdigest(),
            'vegeta': run('vegeta', '-version').strip(), 'images': {}, 'runs': []}
write('metadata.json', metadata)

def volume(label):
    name = network + '-' + label
    docker('volume', 'create', name)
    volumes.append(name)
    return name

def start(name, image, extra, command=None, memory='2g'):
    full = network + '-' + name
    cmd = ['run', '-d', '--name', full, '--network', network, '--cpuset-cpus', server_cpus,
           '--memory', memory, '--memory-swap', memory, *extra, image]
    if command:
        cmd += command
    docker(*cmd)
    containers.append(full)
    return full

def get(url):
    with urllib.request.urlopen(url, timeout=3) as response:
        if response.status != 200 or response.url != url:
            raise RuntimeError('Unexpected redirect or status')
        return response.read()

def ready(url):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            data = get(url)
            if b'GORUN_COMPARISON_BODY' in data:
                return len(data)
        except Exception:
            pass
        time.sleep(.2)
    raise RuntimeError('Application readiness failed: ' + url)

class DockerSocket(http.client.HTTPConnection):
    def connect(self):
        import socket
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect('/var/run/docker.sock')

def stats(name):
    client = DockerSocket('localhost', timeout=5)
    try:
        client.request('GET', '/containers/' + name + '/stats?stream=false&one-shot=true')
        response = client.getresponse()
        if response.status != 200:
            raise RuntimeError('Docker stats failed')
        value = json.load(response)
        mem = value['memory_stats']
        return {'container': name, 'cpu_ns': value['cpu_stats']['cpu_usage']['total_usage'],
                'working_set_bytes': max(0, mem.get('usage',0)-mem.get('stats',{}).get('inactive_file',0))}
    finally:
        client.close()

try:
    for label, image in images.items():
        docker('pull', image)
        info = json.loads(docker('image', 'inspect', image))[0]
        metadata['images'][label] = {'tag': image, 'id': info['Id'], 'digests': info['RepoDigests'], 'size_bytes': info['Size']}
    write('metadata.json', metadata)
    docker('network', 'create', network)
    # Use the actual repository build, not a benchmark-only implementation.
    docker('build', '-q', '-f', 'deploy/Dockerfile', '--build-arg', 'VERSION=benchmark', '-t', network+'-gorun', '.')
    metadata['images']['gorun'] = json.loads(docker('image','inspect',network+'-gorun'))[0]['Id']
    gv = volume('gorun')
    env = ['-e','GORUN_BASE_URL=https://benchmark.example', '-e','GORUN_SECRET_KEY='+base64.b64encode(secrets.token_bytes(32)).decode(),
           '-e','GORUN_SETUP_TOKEN='+secrets.token_hex(16), '-e','GORUN_THEME=benchmark']
    theme = fixture/'gorun-theme'
    theme.mkdir()
    (theme/'theme.json').write_text(json.dumps({'name':'Benchmark','version':'1.0.0','parent':'default'}))
    (theme/'templates').mkdir()
    (theme/'templates/single.html').write_text('{{with .Content}}<!doctype html><html lang="en"><head><title>{{.Title}}</title></head><body><article><h1>{{.Title}}</h1>{{safe .Body}}</article></body></html>{{end}}')
    gextra = ['-v',gv+':/data', '-v',str(theme)+':/app/themes/benchmark:ro', *env]
    def gcli(*command):
        return docker('run','--rm','--entrypoint','/app/gorun',*gextra,network+'-gorun',*command)
    gcli('user','create','--email','benchmark@example.invalid','--password','Synthetic-benchmark-password-2026','--admin')
    raw=gcli('key','create','--name','benchmark','--user','benchmark@example.invalid','--scopes','*','--rate','10000')
    token=json.JSONDecoder().raw_decode(raw[raw.index('{'):])[0]['token']
    gorun=start('gorun',network+'-gorun',[*gextra,'-p','127.0.0.1:18080:8080'])
    for _ in range(300):
        try: get('http://127.0.0.1:18080/healthz'); break
        except Exception: time.sleep(.2)
    for post in posts:
        request=urllib.request.Request('http://127.0.0.1:18080/api/v1/content',data=json.dumps({'type':'post','status':'published',**post}).encode(),headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
        with urllib.request.urlopen(request,timeout=10) as response:
            if response.status not in (200,201): raise RuntimeError('GoRun seeding failed')
    del token
    wpv, dbv = volume('wordpress'), volume('database')
    db_env=['-e','MARIADB_DATABASE=wordpress','-e','MARIADB_USER=wordpress','-e','MARIADB_PASSWORD=synthetic-benchmark-only','-e','MARIADB_ROOT_PASSWORD=synthetic-root-only']
    database=start('db',images['database'],['-v',dbv+':/var/lib/mysql',*db_env],memory='512m')
    wp_env=['-e','WORDPRESS_DB_HOST='+database,'-e','WORDPRESS_DB_USER=wordpress','-e','WORDPRESS_DB_PASSWORD=synthetic-benchmark-only','-e','WORDPRESS_DB_NAME=wordpress', '-e',"WORDPRESS_CONFIG_EXTRA=define('DISABLE_WP_CRON',true); define('AUTOMATIC_UPDATER_DISABLED',true); define('WP_HTTP_BLOCK_EXTERNAL',true);"]
    wordpress=start('wordpress',images['wordpress'],['-v',wpv+':/var/www/html','-p','127.0.0.1:18081:80',*wp_env],memory='1536m')
    def wp(*command):
        return docker('run','--rm','--network',network,'--user','33:33','-e','HOME=/tmp','-v',wpv+':/var/www/html','-v',str(fixture)+':/fixture:ro',*wp_env,images['wpcli'],'wp',*command)
    for _ in range(120):
        try: wp('db','check'); break
        except subprocess.CalledProcessError: time.sleep(1)
    else:
        raise RuntimeError('WordPress database did not become ready')
    wp('core','install','--url=http://127.0.0.1:18081','--title=Benchmark','--admin_user=benchmark','--admin_password=Synthetic-benchmark-only-2026','--admin_email=benchmark@example.invalid','--skip-email')
    (fixture/'seed.php').write_text('''<?php
foreach (json_decode(file_get_contents('/fixture/posts.json'),true) as $p) wp_insert_post(['post_type'=>'post','post_status'=>'publish','post_title'=>$p['title'],'post_name'=>$p['slug'],'post_content'=>$p['body']]);
$dir=WP_CONTENT_DIR.'/themes/benchmark'; wp_mkdir_p($dir);
file_put_contents($dir.'/style.css',"/*\\nTheme Name: Benchmark\\n*/");
file_put_contents($dir.'/index.php','<?php while(have_posts()):the_post(); ?><!doctype html><html lang="en"><head><title><?php echo esc_html(get_the_title()); ?></title></head><body><article><h1><?php echo esc_html(get_the_title()); ?></h1><?php echo get_the_content(); ?></article></body></html><?php endwhile; ?>');
switch_theme('benchmark');
''')
    wp('eval-file','/fixture/seed.php')
    wp('rewrite','structure','/post/%postname%','--hard')
    wp('rewrite','flush','--hard')
    # Static generation is a separate category, served with no Node process.
    astro=fixture/'astro'
    (astro/'src/pages/post').mkdir(parents=True)
    shutil.copy(fixture/'posts.json',astro/'posts.json')
    (astro/'package.json').write_text(json.dumps({'private':True,'type':'module','dependencies':{'astro':'7.4.0'},'scripts':{'build':'astro build'}}))
    (astro/'astro.config.mjs').write_text("export default {output:'static',trailingSlash:'never',build:{format:'file'}};")
    (astro/'src/pages/post/[slug].astro').write_text('''---
import posts from '../../../posts.json';
export function getStaticPaths(){return posts.map(post=>({params:{slug:post.slug},props:post}));}
const {title,body}=Astro.props;
---
<!doctype html><html lang="en"><head><title>{title}</title></head><body><article><h1>{title}</h1><Fragment set:html={body}/></article></body></html>
''')
    build_start=time.monotonic()
    docker('run','--rm','--user',f'{os.getuid()}:{os.getgid()}','-e','HOME=/tmp','--cpuset-cpus',server_cpus,'--memory','2g','-v',str(astro)+':/app','-w','/app',images['node'],'sh','-ec','npm install --ignore-scripts && npm run build')
    metadata['astro_dependency_install_and_build_seconds']=time.monotonic()-build_start
    (fixture/'nginx.conf').write_text('server { listen 80; root /usr/share/nginx/html; location / { try_files $uri.html $uri/index.html =404; } }')
    static=start('astro',images['nginx'],['-v',str(astro/'dist')+':/usr/share/nginx/html:ro','-v',str(fixture/'nginx.conf')+':/etc/nginx/conf.d/default.conf:ro','-p','127.0.0.1:18082:80'])
    products={'gorun':{'containers':[gorun],'port':18080},'wordpress':{'containers':[wordpress,database],'port':18081},'astro-static':{'containers':[static],'port':18082}}
    metadata['wordpress_version']=wp('core','version').strip()
    metadata['php_version']=docker('exec',wordpress,'php','-v').splitlines()[0]
    metadata['gorun_version']=gcli('version').strip()
    for name,p in products.items():
        p['response_bytes']=[ready(f"http://127.0.0.1:{p['port']}/post/{post['slug']}") for post in posts]
        p['targets']=out/(name+'-targets.txt')
        p['targets'].write_text(''.join(f"GET http://127.0.0.1:{p['port']}/post/{post['slug']}\n" for post in posts))
        for container in p['containers']: docker('stop',container)
    metadata['products']={name:{k:v for k,v in p.items() if k!='targets'} for name,p in products.items()}
    for repetition in range(3):
        order=list(products)[repetition:]+list(products)[:repetition]
        for name in order:
            p=products[name]
            started=time.monotonic()
            for container in reversed(p['containers']): docker('start',container)
            ready(f"http://127.0.0.1:{p['port']}/post/post-0001")
            startup=time.monotonic()-started
            attack=['taskset','-c',load_cpus,'vegeta','attack','-targets='+str(p['targets']),'-header=Accept-Encoding: identity','-max-workers=100']
            subprocess.run([*attack,'-rate=50','-duration=5s'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            idle=[stats(c) for c in p['containers']]
            for rate in (50,200):
                label=f'{name}-r{repetition+1}-{rate}rps'
                samples=[];stop=threading.Event()
                def sample():
                    while not stop.is_set():
                        samples.append({'elapsed_seconds':time.monotonic()-begin,'stats':[stats(c) for c in p['containers']]})
                        stop.wait(1)
                before=[stats(c) for c in p['containers']];begin=time.monotonic()
                thread=threading.Thread(target=sample);thread.start()
                try:
                    with (out/(label+'.bin')).open('wb') as raw:
                        subprocess.run([*attack,f'-rate={rate}','-duration=30s'],check=True,stdout=raw,stderr=subprocess.PIPE)
                finally:
                    stop.set();thread.join()
                elapsed=time.monotonic()-begin;after=[stats(c) for c in p['containers']]
                report=json.loads(run('vegeta','report','-type=json',str(out/(label+'.bin'))))
                write(label+'-report.json',report);write(label+'-samples.json',samples)
                metadata['runs'].append({'product':name,'repetition':repetition+1,'offered_rps':rate,'startup_seconds':startup,'elapsed_seconds':elapsed,'idle':idle,'before':before,'after':after,'report_file':label+'-report.json','samples_file':label+'-samples.json'})
                write('metadata.json',metadata)
            for container in p['containers']: docker('stop',container)
    metadata['status']='complete'
    write('metadata.json',metadata)
    print('Complete: raw measurements saved. Validate before publishing comparisons.')
except Exception as error:
    metadata['failure_type']=type(error).__name__
    # Keep failures inspectable without dumping command lines or credentials.
    if isinstance(error,subprocess.CalledProcessError):
        metadata['failure_detail']=(error.stderr or b'').decode(errors='replace')[-2000:]
    else:
        metadata['failure_detail']=str(error)[:1000]
    write('metadata.json',metadata)
    raise SystemExit('Benchmark incomplete; inspect metadata.json. No comparison claims may be published.')
finally:
    shutil.rmtree(fixture/'astro/node_modules',ignore_errors=True)
    for container in reversed(containers):
        subprocess.run(['docker','rm','-f',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    for name in volumes:
        subprocess.run(['docker','volume','rm',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    subprocess.run(['docker','network','rm',network],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
