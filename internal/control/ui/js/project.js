import { api, setStorageProject } from './core.js';

// Resolve project identity exactly once before any project-scoped local state is
// read. Both main navigation and cross-feature Map actions share this boundary.
async function activeProjectIdentity(){
  try{
    const project=await api('/api/project');
    if(project&&project.current)return project.current;
  }catch(e){}
  try{
    const version=await api('/api/version');
    if(version&&version.project)return version.project;
  }catch(e){}
  return 'default';
}

export const projectStorageReady=activeProjectIdentity().then(name=>setStorageProject(name));

let mapMod=null;
export function loadMapModule(){
  return projectStorageReady.then(()=>mapMod||(mapMod=import('./map.js')));
}
