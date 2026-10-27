#!/usr/bin/env python3
"""Actual hub + Linux desktop engine + nested PC TLS. No physical clipboard."""
import argparse, base64, os, hashlib, http.client, io, json, re, signal, socket, ssl, subprocess, tempfile, time, urllib.request, urllib.error
from pathlib import Path
from hub_smoke import available_port, PinnedClient

class InnerTLS:
    def __init__(self, outer, certificate, pin):
        self.outer=outer; self.incoming=ssl.MemoryBIO(); self.outgoing=ssl.MemoryBIO()
        context=ssl.create_default_context(cafile=str(certificate))
        self.tls=context.wrap_bio(self.incoming,self.outgoing,server_side=False,server_hostname="localhost")
        self.perform(self.tls.do_handshake)
        assert hashlib.sha256(self.tls.getpeercert(binary_form=True)).hexdigest()==pin, "PC pin mismatch"
    def drain(self):
        while self.outgoing.pending:self.outer.sendall(self.outgoing.read())
    def perform(self, action):
        while True:
            try:
                result=action();self.drain();return result
            except ssl.SSLWantReadError:
                self.drain();data=self.outer.recv(65536)
                if not data:self.incoming.write_eof()
                else:self.incoming.write(data)
            except ssl.SSLWantWriteError:self.drain()
    def sendall(self,data):
        while data:data=data[self.perform(lambda:self.tls.write(data)):]
    def recv(self,n):return self.perform(lambda:self.tls.read(n))
    def makefile(self,*_):
        owner=self
        class Reader(io.RawIOBase):
            def readable(self):return True
            def readinto(self,b):
                data=owner.recv(len(b));b[:len(data)]=data;return len(data)
        return io.BufferedReader(Reader())

def main():
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--hub-bin',required=True,type=Path);p.add_argument('--desktop-bin',required=True,type=Path);p.add_argument('--apk',type=Path);p.add_argument('--output',type=Path);a=p.parse_args()
    checks=[];processes=[]
    with tempfile.TemporaryDirectory(prefix='onespace-connect-') as temp:
        root=Path(temp);hubdir=root/'hub';hubdir.mkdir(mode=0o700);desk=root/'desktop';desk.mkdir(mode=0o700)
        def admin(*args):
            out=subprocess.run([str(a.hub_bin.resolve()),args[0],'--data',str(hubdir),*args[1:]],capture_output=True,text=True,check=True)
            return json.loads(out.stdout)
        pc=admin('enroll','--id','windows','--name','PC','--kind','windows','--scopes','connect,clipboard,updates,updates.publish')
        phone=admin('enroll','--id','phone','--name','Phone','--kind','android','--scopes','connect,clipboard,updates')
        port=available_port();profile={'address':f'https://localhost:{port}','fingerprint':pc['fingerprint'],'enabled':True,'connect':True,'clipboardMode':'lan','target':'phone','receiveClipboard':False,'overlayCIDRs':[],'protectedToken':base64.b64encode(pc['token'].encode()).decode()}
        (desk/'hub.json').write_text(json.dumps(profile));(desk/'hub.json').chmod(0o600)
        logs=[]
        def launch(binary,args,name):
            log=(root/name).open('w');logs.append(log);process=subprocess.Popen([str(binary.resolve()),*args],stdout=log,stderr=log);processes.append(process);return process
        local=urllib.request.build_opener(urllib.request.ProxyHandler({}))
        try:
            hub=launch(a.hub_bin,['serve','--data',str(hubdir),'--listen',f'127.0.0.1:{port}'],'hub.log')
            client=PinnedClient(port,hubdir/'identity.crt',pc['fingerprint'])
            deadline=time.monotonic()+8
            while True:
                try:client.call('/hub/v1/info');break
                except OSError:
                    assert hub.poll() is None
                    if time.monotonic()>deadline:raise
                    time.sleep(.05)
            engine=launch(a.desktop_bin,['-no-browser','-data',str(desk)],'desktop.log')
            while True:
                try:page=local.open('http://127.0.0.1:47600/',timeout=1).read().decode();break
                except OSError:
                    assert engine.poll() is None
                    if time.monotonic()>deadline:raise
                    time.sleep(.05)
            key=re.search(r"const KEY='([a-f0-9]{48})'",page).group(1)
            def local_call(path,body=None,expected=200,raw=None):
                payload=raw if raw is not None else json.dumps(body).encode() if body is not None else None
                request=urllib.request.Request('http://127.0.0.1:47600'+path,payload,{'X-Local-Key':key,'Content-Type':'application/octet-stream' if raw else 'application/json'},method='POST' if payload is not None else 'GET')
                try:res=local.open(request,timeout=10)
                except urllib.error.HTTPError as e:res=e
                with res:
                    value=res.read();assert res.status==expected,f'{path}: {res.status} {value[:200]}'
                    return json.loads(value)
            info=local_call('/local/overview');pin=info['fingerprint'];local_call('/local/password',{'password':'fixture-private-password'})
            def pc_call(path,method='GET',value=None,token='',expected=200):
                context=ssl.create_default_context(cafile=str(hubdir/'identity.crt'))
                deadline=time.monotonic()+8
                while True:
                    outer=context.wrap_socket(socket.create_connection(('localhost',port),timeout=8),server_hostname='localhost')
                    assert hashlib.sha256(outer.getpeercert(binary_form=True)).hexdigest()==pc['fingerprint']
                    outer.sendall((f'POST /hub/v1/connect/open?to=windows HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer {phone["token"]}\r\nConnection: Upgrade\r\nUpgrade: onespace-connect-v1\r\nContent-Length: 0\r\n\r\n').encode())
                    header=b''
                    while not header.endswith(b'\r\n\r\n'):
                        byte=outer.recv(1)
                        if not byte:raise AssertionError('upgrade closed')
                        header+=byte;assert len(header)<16384
                    if header.startswith(b'HTTP/1.1 101 '):break
                    outer.close()
                    assert time.monotonic()<deadline,'PC did not register relay listeners'
                    time.sleep(.1)
                try:
                    inner=InnerTLS(outer,desk/'identity.crt',pin)
                    payload=json.dumps(value).encode() if value is not None else b''
                    auth=f'Authorization: Bearer {token}\r\n' if token else ''
                    inner.sendall((f'{method} {path} HTTP/1.1\r\nHost: localhost:47601\r\n{auth}Content-Type: application/json\r\nContent-Length: {len(payload)}\r\nConnection: close\r\n\r\n').encode()+payload)
                    response=http.client.HTTPResponse(inner);response.begin();data=response.read();assert response.status==expected,f'{path}: {response.status} {data[:100]}'
                    return json.loads(data)
                finally:outer.close()
            assert pc_call('/v1/info')['app']=='OneSpace'
            checks.append('actual PC TLS identity verified through actual hub reverse relay')
            pc_call('/v1/items',expected=401)
            checks.append('hub enrollment alone cannot bypass PC pairing')
            pending=pc_call('/v1/pair/request','POST',{'password':'fixture-private-password','name':'Relay phone'},expected=202)
            local_call('/local/pair/decision',{'id':pending['id'],'approve':True})
            approved=pc_call('/v1/pair/status?id='+pending['id']+'&ticket='+pending['ticket'])
            token=approved['token'];checks.append('existing PC pairing and local approval work through relay')
            note=pc_call('/v1/notes','POST',{'title':'Relay note','body':'Private fixture note'},token,201)
            assert any(n['id']==note['id'] for n in pc_call('/v1/notes',token=token))
            item=pc_call('/v1/items/text','POST',{'text':'Private relay fixture','bucket':'Temporary'},token,201)
            assert any(x['id']==item['id'] for x in pc_call('/v1/items',token=token))
            checks.append('workspace and item transfer use unchanged PC protocol through relay')
            local_call('/local/device/permission',{'id':pending['id'],'feature':'notes','enabled':False})
            pc_call('/v1/notes',token=token,expected=403)
            checks.append('PC permissions remain enforced inside relay')
            if a.apk:
                manifest=local_call('/local/hub/updates/publish',raw=a.apk.read_bytes(),expected=201)
                local_call('/local/hub/updates/publish',raw=a.apk.read_bytes(),expected=409)
                checks.append('desktop streams APK to Pi; duplicate version publication rejected')
                engine.send_signal(signal.SIGINT);engine.wait(timeout=10)
                received,_=client.call('/hub/v1/updates/manifest',token=phone['token'])
                apk,_=client.call('/hub/v1/updates/apk',token=phone['token'])
                assert received==manifest and hashlib.sha256(apk).hexdigest()==manifest['sha256']
                checks.append('Pi serves verified APK bytes with Windows process off')
                env=os.environ.copy();env.update({'ONESPACE_HUB_URL':f'https://localhost:{port}','ONESPACE_HUB_FINGERPRINT':pc['fingerprint'],'ONESPACE_HUB_TOKEN':pc['token']})
                reserved=subprocess.run([__import__('sys').executable,str(Path(__file__).resolve().parents[1]/'tools/reserve-android-version.py'),'--minimum-code','24'],env=env,capture_output=True,text=True,check=True)
                assert json.loads(reserved.stdout)['versionCode']>manifest['versionCode']
                checks.append('shared code helper verifies Pi identity and allocates above published code')

        finally:
            for process in reversed(processes):
                if process.poll() is None:process.send_signal(signal.SIGINT);process.wait(timeout=12)
            for log in logs:log.close()
    report={'status':'passed','checks':checks,'limits':'Linux engine stubs; not physical Windows paste, Android lifecycle, Pi camera or public-network deployment'}
    if a.output:a.output.write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
if __name__=='__main__':main()
