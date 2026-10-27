#!/usr/bin/env python3
"""Validate the actual Gradle shared allocator against an isolated hub.
Uses Gradle dry-run: reserves codes without creating/signing a release.
"""
import argparse,json,os,re,signal,subprocess,tempfile,time
from pathlib import Path
from hub_smoke import available_port,PinnedClient
p=argparse.ArgumentParser(description=__doc__);p.add_argument('--hub-bin',required=True,type=Path);p.add_argument('--output',type=Path);a=p.parse_args()
root=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix='onespace-code-fixture-') as temp:
 data=Path(temp)/'hub';data.mkdir(mode=0o700)
 admin=subprocess.run([str(a.hub_bin.resolve()),'enroll','--data',str(data),'--id','builder','--name','Fixture builder','--kind','linux','--scopes','updates.publish'],capture_output=True,text=True,check=True)
 credential=json.loads(admin.stdout);port=available_port()
 log=(Path(temp)/'hub.log').open('w')
 process=subprocess.Popen([str(a.hub_bin.resolve()),'serve','--data',str(data),'--listen',f'127.0.0.1:{port}'],stdout=log,stderr=log)
 try:
  client=PinnedClient(port,data/'identity.crt',credential['fingerprint'])
  for _ in range(100):
   try:client.call('/hub/v1/info');break
   except OSError:time.sleep(.05)
  env=os.environ.copy();env.update({'ONESPACE_SHARED_VERSION_CODES':'true','ONESPACE_HUB_URL':f'https://localhost:{port}','ONESPACE_HUB_FINGERPRINT':credential['fingerprint'],'ONESPACE_HUB_TOKEN':credential['token']})
  command=['gradlew.bat' if os.name=='nt' else './gradlew',':app:assembleRelease','--dry-run','--offline','--no-daemon']
  build=subprocess.run(command,cwd=root/'android',env=env,capture_output=True,text=True,timeout=120)
  if build.returncode:raise RuntimeError('Gradle shared allocation failed: '+build.stdout[-2000:]+build.stderr[-1000:])
  code=int(re.search(r'versionCode (\d+)',build.stdout).group(1))
  helper=subprocess.run([__import__('sys').executable,str(root/'tools/reserve-android-version.py'),'--minimum-code','24'],env=env,capture_output=True,text=True,check=True)
  following=json.loads(helper.stdout)['versionCode'];assert following>code
  report={'status':'passed','checks':['actual Gradle packaging configuration reserves through pinned TLS','Python helper shares the same increasing authority'],'limits':'dry-run and temporary hub; no release signed or published'}
  if a.output:a.output.write_text(json.dumps(report,indent=2)+'\n')
  print(json.dumps(report,indent=2))
 finally:
  process.send_signal(signal.SIGINT);process.wait(timeout=10);log.close()
