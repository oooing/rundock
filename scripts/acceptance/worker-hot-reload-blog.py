"""Exercise the real blog source snapshot through RunDock (no mock API).
Requires isolated backend :18751 and the copied blog under the output folder.
Stages: start, reload, fail, recover. Fault injection only edits the copy.
"""
import json, os, sys, time, urllib.request
from pathlib import Path

sys.stdout.reconfigure(encoding='utf-8')
root = Path(__file__).resolve().parents[2]
out = root / 'outputs/acceptance/hot-reload-blog-20261103'
worker = out / 'blog/code/server/worker.js'
report_path = Path(os.environ.get('RUNDOCK_ACCEPTANCE_REPORT', str(out / 'report.json')))
base = os.environ.get('RUNDOCK_ACCEPTANCE_BASE', 'http://127.0.0.1:18751')
report = json.loads(report_path.read_text(encoding='utf-8')) if report_path.exists() else {'checks': []}

def api(path, body=None):
    req = urllib.request.Request(base+path,
        data=None if body is None else json.dumps(body).encode(),
        headers={'Origin':'http://127.0.0.1:18752','Content-Type':'application/json'})
    with urllib.request.urlopen(req,timeout=25) as response: return json.load(response)

def view(): return api('/api/apps/'+report['appId'])
def wait(predicate, timeout=180):
    deadline = time.monotonic()+timeout
    seen=[]
    while time.monotonic()<deadline:
        current=view()
        seen.append(current['status'])
        if predicate(current): return current, seen
        if current['status']=='failed': raise RuntimeError('Blog startup failed; inspect RunDock logs')
        time.sleep(2)
    raise TimeoutError(json.dumps(current,ensure_ascii=False))

def snapshot(label, current, states):
    report['checks'].append(label)
    report[label]={'status':current['status'],'runId':current['runId'],
        'services':[{k:s.get(k) for k in ['port','statusScope','health','healthReason']} for s in current['services']],
        'observedStates':sorted(set(states))}
    report_path.write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
    print(label,json.dumps(report[label],ensure_ascii=False),flush=True)

stage=sys.argv[1]
if stage=='start':
    existing = api('/api/apps')
    if os.environ.get('RUNDOCK_ACCEPTANCE_INSTALLED') != '1':
        assert existing==[], 'Fresh isolated database required'
    assert not any(a['entryScript']==str(out/'blog/启动博客.cmd') for a in existing), 'Acceptance card already exists'
    candidate=api('/api/import',{'scriptPath':str(out/'blog/启动博客.cmd')})
    candidate['name']='oooing 博客（安装版验收）' if os.environ.get('RUNDOCK_ACCEPTANCE_INSTALLED') == '1' else 'oooing 博客（热重载验收）'
    candidate['cwd']=str(out/'blog')
    candidate['accessLinks']=[{'name':'博客首页','url':'http://127.0.0.1:17653/'},{'name':'管理后台','url':'http://127.0.0.1:17653/admin'}]
    candidate={k:v for k,v in candidate.items() if k in ['name','entryScript','cwd','adapterType','cmd','args','env','tags','portHints','healthUrl','scriptHash','accessLinks']}
    created=api('/api/apps',candidate); report['appId']=created['id']
    report_path.write_text(json.dumps(report),encoding='utf-8')
    api('/api/apps/'+created['id']+'/start',{})
if stage in ['start','baseline']:
    current,states=wait(lambda a:a['status']=='running' and any(s['statusScope']=='auxiliary' for s in a['services']) and {17653,8788}.issubset({s['port'] for s in a['services'] if s['statusScope']=='required' and s['health']=='healthy'}))
    required={s['port'] for s in current['services'] if s['statusScope']=='required'}
    assert {17653,8788}.issubset(required), required
    snapshot('initial',current,states)
elif stage=='reload':
    old={s['port'] for s in view()['services'] if s['statusScope']=='auxiliary'}
    assert old
    worker.write_text(worker.read_text(encoding='utf-8')+'\n// RunDock isolated hot reload acceptance\n',encoding='utf-8')
    current,states=wait(lambda a:a['status']=='running' and any(s['statusScope']=='auxiliary' and s['port'] not in old for s in a['services']) and not old.intersection(s['port'] for s in a['services']))
    assert 'degraded' not in states, states
    report['retiredPorts']=sorted(old)
    snapshot('hotReload',current,states)
elif stage=='fail':
    original=worker.read_text(encoding='utf-8')
    (out/'worker-before-fault.txt').write_text(original,encoding='utf-8')
    needle='export async function handle(request, env) {'
    assert needle in original
    injected=original.replace(needle,needle+"\n  if (new URL(request.url).pathname === '/api/health') return new Response('RunDock acceptance: backend unavailable', {status: 503});",1)
    worker.write_text(injected,encoding='utf-8')
    current,states=wait(lambda a:a['status']=='degraded' and any(s['port']==8788 and s['health']=='unhealthy' and s.get('healthReason')=='http_status:503' for s in a['services']))
    snapshot('backendFailure',current,states)
elif stage=='recover':
    worker.write_text((out/'worker-before-fault.txt').read_text(encoding='utf-8'),encoding='utf-8')
    current,states=wait(lambda a:a['status']=='running' and any(s['port']==8788 and s['health']=='healthy' for s in a['services']))
    snapshot('recovered',current,states)
else: raise ValueError(stage)
