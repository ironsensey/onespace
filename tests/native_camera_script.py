#!/usr/bin/env python3
"""Check embedded video JS and its actual Go RPC arities without a browser.
Does not establish real WebRTC media or Windows WebView2 execution.
"""
import json,re,subprocess
from pathlib import Path
root=Path(__file__).resolve().parents[1]
source=(root/'windows/cmd/onespace/native_camera_windows.go').read_text()
html=re.search(r'const nativeCameraHTML = `(.*?)`',source,re.S).group(1)
script=re.search(r'<script>(.*?)</script>',html,re.S).group(1).replace('__VIEWER_NONCE__','fixture-proof')
arities={name:len(parameters.split(',')) for name,parameters in re.findall(r'w.Bind\("(\w+)", func\((.*?)\)',source)}
harness=r'''
const vm=require('vm'),assert=require('assert');let input='';process.stdin.on('data',x=>input+=x);process.stdin.on('end',async()=>{
 const fixture=JSON.parse(input),elements={},events={},calls=[];
 const context={console,setTimeout,clearTimeout,MediaStream:class{},window:{addEventListener:(n,f)=>events[n]=f},document:{hidden:false,getElementById:id=>elements[id]||(elements[id]={}),addEventListener:(n,f)=>events[n]=f}};
 for(const [name,count] of Object.entries(fixture.arities))context[name]=async(...args)=>{assert.equal(args.length,count, name+' Go/JS arity mismatch');assert.equal(args[0],'fixture-proof');calls.push(name);return name==='openCamera'?'v=0\r\ns=fixture-answer\r\n':undefined};
 context.RTCPeerConnection=class{constructor(){this.iceGatheringState='complete'}addTransceiver(){}async createOffer(){return{type:'offer',sdp:'v=0\r\ns=fixture-offer\r\n'}}async setLocalDescription(x){this.localDescription=x}async setRemoteDescription(x){assert.equal(x.type,'answer');this.connectionState='connected';this.onconnectionstatechange()}close(){}};
 vm.runInNewContext(fixture.script,context);
 await elements.start.onclick();assert.equal(elements.s.textContent,'Video connection: connected');await elements.stop.onclick();assert.deepEqual(calls,['closeCamera','openCamera','closeCamera']);assert.equal(elements.v.srcObject,null);
 console.log(JSON.stringify({status:'passed',checks:['video script connects and stops','Go RPC argument contracts match','per-viewer proof accompanies every RPC'],limits:'mock WebRTC; no actual video or native Windows runtime'}));
});
'''
subprocess.run(['node','-e',harness],input=json.dumps({'script':script,'arities':arities}),text=True,check=True)
