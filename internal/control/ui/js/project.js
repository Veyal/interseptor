import { api, setStorageProject } from './core.js';

// Resolve project identity exactly once before any project-scoped local state is
// read. Both main navigation and cross-feature Map actions share this boundary.
const PROJECT_IDENTITY_TIMEOUT_MS=2500;
async function activeProjectIdentity(){
  const controller=new AbortController();
  const timer=setTimeout(()=>controller.abort(),PROJECT_IDENTITY_TIMEOUT_MS);
  try{
    const opts={signal:controller.signal,cache:'no-store'};
    const [projectResult,versionResult]=await Promise.allSettled([
      api('/api/project',opts),
      api('/api/version',opts),
    ]);
    const project=projectResult.status==='fulfilled'&&projectResult.value;
    const version=versionResult.status==='fulfilled'&&versionResult.value;
    if(project&&typeof project.current==='string'&&project.current.trim())return project.current;
    if(version&&typeof version.project==='string'&&version.project.trim())return version.project;
    throw new Error('active project unavailable');
  }finally{clearTimeout(timer);}
}

export const projectStorageReady=activeProjectIdentity().then(name=>setStorageProject(name));

let mapMod=null;
export function loadMapModule(){
  return projectStorageReady.then(()=>mapMod||(mapMod=import('./map.js')));
}
