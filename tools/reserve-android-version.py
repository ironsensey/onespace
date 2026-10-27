#!/usr/bin/env python3
"""Reserve a shared code through exact pinned TLS; optionally build unsigned release.

Set ONESPACE_HUB_URL, ONESPACE_HUB_FINGERPRINT, ONESPACE_HUB_TOKEN privately.
The token requires updates.publish. No token is printed or passed to Gradle.
"""
import argparse, hashlib, http.client, json, os, ssl, subprocess
from pathlib import Path
from urllib.parse import urlsplit
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--minimum-code',type=int,default=29)
p.add_argument('--build-release',action='store_true')
a=p.parse_args()
u=urlsplit(os.environ['ONESPACE_HUB_URL'])
assert u.scheme=='https' and u.hostname and not u.username and u.path in ('','/') and not u.query and not u.fragment,'Use a hub HTTPS origin'
pin=os.environ['ONESPACE_HUB_FINGERPRINT'].lower()
assert len(pin)==64 and all(c in '0123456789abcdef' for c in pin),'Invalid fingerprint'
context=ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
context.check_hostname=False;context.verify_mode=ssl.CERT_NONE
# Exact certificate pin replaces CA/DNS validation, before any token is sent.
c=http.client.HTTPSConnection(u.hostname,u.port or 443,context=context,timeout=12)
try:
 c.connect()
 certificate=c.sock.getpeercert(binary_form=True)
 if hashlib.sha256(certificate).hexdigest()!=pin:raise RuntimeError('Hub identity mismatch')
 c.close()
 trusted=ssl.create_default_context()
 trusted.check_hostname=False
 trusted.load_verify_locations(cadata=ssl.DER_cert_to_PEM_cert(certificate))
 c=http.client.HTTPSConnection(u.hostname,u.port or 443,context=trusted,timeout=12)
 c.connect()
 if hashlib.sha256(c.sock.getpeercert(binary_form=True)).hexdigest()!=pin:raise RuntimeError('Hub identity mismatch')
 c.request('POST','/hub/v1/updates/reserve',json.dumps({'minimumCode':a.minimum_code}),{'Authorization':'Bearer '+os.environ['ONESPACE_HUB_TOKEN'],'Content-Type':'application/json'})
 r=c.getresponse();data=r.read(4097)
 if r.status!=201 or len(data)>4096:raise RuntimeError('Version reservation failed; verify hub access and publisher scope')
 code=int(json.loads(data)['versionCode'])
finally:c.close()
print(json.dumps({'versionCode':code}))
if a.build_release:
 android=Path(__file__).resolve().parents[1]/'android'
 wrapper='gradlew.bat' if os.name=='nt' else './gradlew'
 environment=os.environ.copy();environment.pop('ONESPACE_HUB_TOKEN',None)
 subprocess.run([wrapper,f'-PonespaceVersionCode={code}',':app:assembleRelease'],cwd=android,env=environment,check=True)
