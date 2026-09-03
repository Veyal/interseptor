import { api, setStorageProject } from './core.js';

// Resolve project identity exactly once before any project-scoped local state is
// read. Both main navigation and cross-feature Map actions share this boundary.
const PROJECT_IDENTITY_TIMEOUT_MS=2500;
async function activeProjectIdentity(){
  const controller=new AbortController();
  let timer;
  const deadline=new Promise((_,reject)=>{
    timer=setTimeout(()=>{
      controller.abort();
      reject(new Error('active project timed out'));
    },PROJECT_IDENTITY_TIMEOUT_MS);
  });
  try{
    const opts={signal:controller.signal,cache:'no-store'};
    const requests=Promise.allSettled([
      api('/api/project',opts),
      api('/api/version',opts),
    ]);
    const [projectResult,versionResult]=await Promise.race([requests,deadline]);
    const project=projectResult.status==='fulfilled'&&projectResult.value;
    const version=versionResult.status==='fulfilled'&&versionResult.value;
    if(project&&typeof project.current==='string'&&project.current.trim()){
      const projects=Array.isArray(project.projects)?project.projects.map(entry=>entry&&entry.name).filter(name=>typeof name==='string'&&name.trim()):[];
      return {name:project.current,projects};
    }
    if(version&&typeof version.project==='string'&&version.project.trim())return {name:version.project,projects:[]};
    throw new Error('active project unavailable');
  }finally{clearTimeout(timer);}
}

export const projectStorageReady=activeProjectIdentity().then(identity=>setStorageProject(identity.name,identity.projects));

let mapMod=null;
export function loadMapModule(){
  return projectStorageReady.then(()=>mapMod||(mapMod=import('./map.js')));
}
