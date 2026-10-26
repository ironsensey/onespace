// Bundled into the local authenticated dashboard; no third-party scripts.
let cloudPending=[],cloudNext='',cloudBusy=false,cloudPaused=false;
const cloudId=id=>/^[A-Za-z0-9_-]{8,200}$/.test(id);
function cloudPolicy(){const days=Number(el('cloudDays').value);if(!Number.isInteger(days)||days<1||days>365)throw Error('Choose 1–365 temporary days');return {bucket:el('cloudBucket').value,days}}
async function cloudStatus(){
 const s=await api('/hub/cloud/status');cloudPending=s.jobs||[];
 el('cloudStatus').textContent=s.credentialsPresent?'Google credential saved on Pi. Refresh files to verify current Google access.':'Google Drive is not connected. Follow DRIVE-SETUP.md on your Pi.';
 const r=s.recording;el('recordState').textContent=(s.recordingActive?'Recorder enabled in MediaMTX':r.enabled?'Recording requested; waiting or paused':'Recording off')+' · '+human(s.spoolBytes)+' buffered'+(s.recordingError?' · '+s.recordingError:'')+(s.uploadError?' · Upload: '+s.uploadError:'')+(s.retentionError?' · Cleanup: '+s.retentionError:'')+'. Check completed clips to verify footage is arriving.';
 el('cloudJobs').innerHTML=cloudPending.map(j=>`<div class="entry"><div><b>${esc(j.name)}</b><div class="small muted">${human(j.offset)} / ${human(j.size)} · ${esc(j.bucket)} · ${esc(j.kind)}</div></div>${j.kind==='file'?`<div class="fl"><button class="button slim" data-cloudresume="${esc(j.id)}">Resume</button><button class="button slim danger" data-cloudcancel="${esc(j.id)}">Cancel</button></div>`:''}</div>`).join('')||'<p class="muted">No pending uploads</p>';
}
function cloudRow(f){if(!cloudId(f.id))return '';const p=f.appProperties||{};return `<div class="entry"><div><b>${esc(f.name)}</b><div class="small muted">${human(Number(f.size)||0)} · ${esc(p.kind||'file')} · ${esc(p.bucket||'Unknown')}${p.expiresAt?' · expires '+esc(dt(p.expiresAt)):''}</div></div><div class="fl"><a class="button slim" href="https://drive.google.com/file/d/${encodeURIComponent(f.id)}/view" target="_blank" rel="noopener noreferrer">Open in Drive</a><button class="button slim" data-cloudkeep="${esc(f.id)}">Keep permanently</button><button class="button slim" data-cloudtemp="${esc(f.id)}">Make temporary</button><button class="button slim danger" data-clouddelete="${esc(f.id)}">Move to Trash</button></div></div>`}
async function cloudFiles(more=false){const data=await api('/hub/cloud/files'+(more&&cloudNext?'?pageToken='+encodeURIComponent(cloudNext):''));if(!more)el('cloudFiles').innerHTML='';el('cloudFiles').insertAdjacentHTML('beforeend',(data.files||[]).map(cloudRow).join(''));if(!el('cloudFiles').children.length)el('cloudFiles').textContent='No OneSpace files yet';cloudNext=data.nextPageToken||'';el('cloudMore').classList.toggle('hide',!cloudNext)}
el('cloudRefresh').onclick=async()=>{try{await cloudStatus();const a=await api('/hub/cloud/account');el('cloudStatus').textContent='Connected: '+(a.user.emailAddress||a.user.displayName)+' · Used '+human(Number(a.storageQuota.usage)||0)+(a.storageQuota.limit?' / '+human(Number(a.storageQuota.limit)):'')+' · Trash '+human(Number(a.storageQuota.usageInDriveTrash)||0);await cloudFiles()}catch(e){toast(e.message)}};
el('cloudMore').onclick=()=>cloudFiles(true).catch(e=>toast(e.message));
el('recordStart').onclick=async()=>{try{const p=cloudPolicy();await api('/hub/cloud/recording','POST',{enabled:true,bucket:p.bucket,temporaryDays:p.days});toast('Recording requested. Refresh status to verify the recorder.');await cloudStatus()}catch(e){toast(e.message)}};
el('recordStop').onclick=async()=>{try{await api('/hub/cloud/recording','POST',{enabled:false,bucket:'Temporary',temporaryDays:7});toast('Recording stopped; completed clips will continue uploading');await cloudStatus()}catch(e){toast(e.message)}};
el('cloudPause').onclick=()=>{cloudPaused=true;el('cloudProgress').textContent='Pausing after the current chunk. Choose the same file to resume.'};
async function cloudUploadFile(file,existing){
 if(cloudBusy)throw Error('An upload is already active');if(!file||file.size<=0)throw Error('Choose a non-empty file');if(existing&&(file.name!==existing.name||file.size!==existing.size))throw Error('Choose the same original file name and size');
 cloudBusy=true;cloudPaused=false;let job=existing;let done=false;
 try{
  if(!job){const p=cloudPolicy();job=await api('/hub/cloud/uploads','POST',{name:file.name,size:file.size,bucket:p.bucket,days:p.days})}
  // Resume probes Google before choosing an offset; a lost response can have committed bytes.
  let failures=0,probe=true;
  while(!done&&!cloudPaused){
   try{
    if(probe){let result;try{result=await api('/hub/cloud/uploads/'+job.id)}catch(e){try{const f=await api('/hub/cloud/files/'+job.driveId);if(Number(f.size)===job.size){done=true;break}}catch{}throw e}job=result.job;done=result.complete;if(done)break;probe=false}
    const chunk=await file.slice(job.offset,job.offset+1048576).arrayBuffer();
    const response=await fetch('/local/hub/cloud/uploads/'+job.id,{method:'PUT',headers:{'X-Local-Key':KEY,'Content-Type':'application/octet-stream','X-Upload-Offset':String(job.offset)},body:chunk});
    const result=await response.json();if(!response.ok)throw Error(result.error||'Chunk upload failed');job=result.job;done=result.complete;failures=0;
    el('cloudProgress').textContent=file.name+' · '+Math.floor(job.offset/file.size*100)+'% · '+human(job.offset)+' / '+human(file.size);
   }catch(e){probe=true;if(++failures>=3)throw e;await new Promise(resolve=>setTimeout(resolve,1000*failures))}
  }
  el('cloudProgress').textContent=done?'Saved and verified in Google Drive.':'Upload paused. Choose the same original file to resume.';
 }finally{cloudBusy=false;await cloudStatus().catch(()=>{});if(done)await cloudFiles().catch(()=>{})}
}
el('cloudUpload').onclick=()=>cloudUploadFile(el('cloudFile').files[0]).catch(e=>toast(e.message));
el('cloudCreate').onclick=()=>{const text=el('cloudTextBody').value;if(new TextEncoder().encode(text).length>65536)return toast('Text file limited to 64 KiB; use file upload for larger content');cloudUploadFile(new File([text],el('cloudTextName').value,{type:'text/plain'})).catch(e=>toast(e.message))};
document.body.addEventListener('click',async event=>{
 const button=event.target.closest('button');if(!button)return;const d=button.dataset;
 try{
  if(d.cloudresume){const job=cloudPending.find(j=>j.id===d.cloudresume);if(job)await cloudUploadFile(el('cloudFile').files[0],job);return}
  if(d.cloudcancel){if(cloudBusy)throw Error('Pause the active upload first');if(!confirm('Cancel this pending upload? A completed remote copy, if present, will move to Trash.'))return;await api('/hub/cloud/uploads/'+d.cloudcancel,'DELETE');await cloudStatus()}
  if(d.clouddelete){if(!confirm('Move this OneSpace file to Google Drive Trash?'))return;await api('/hub/cloud/files/'+d.clouddelete,'DELETE');await cloudFiles()}
  if(d.cloudkeep||d.cloudtemp){const p=cloudPolicy();await api('/hub/cloud/files/'+(d.cloudkeep||d.cloudtemp),'PATCH',{bucket:d.cloudkeep?'Permanent':'Temporary',days:p.days});await cloudFiles()}
 }catch(e){toast(e.message)}
});
document.querySelector('[data-tab="cloud"]').addEventListener('click',()=>cloudStatus().catch(e=>toast(e.message)));
window.addEventListener('pagehide',()=>{cloudPaused=true});

setInterval(()=>{if(tab==='cloud'&&!document.hidden&&!cloudBusy)cloudStatus().catch(()=>{})},15000);
