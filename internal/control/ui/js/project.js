import { api, setStorageProject } from './core.js';

// Resolve project identity exactly once before any project-scoped local state is
// read. Both main navigation and cross-feature Map actions share this boundary.
const PROJECT_IDENTITY_TIMEOUT_MS=2500;
async function boundedProjectIdentity(path,select){
  const controller=new AbortController();
  let timer;
  const deadline=new Promise((_,reject)=>{
    timer=setTimeout(()=>{
      controller.abort();
      reject(new Error('active project timed out'));
    },PROJECT_IDENTITY_TIMEOUT_MS);
  });
  try{
    const value=await Promise.race([api(path,{signal:controller.signal,cache:'no-store'}),deadline]);
    const identity=select(value);
    if(identity)return identity;
    throw new Error('active project unavailable');
  }finally{clearTimeout(timer);controller.abort();}
}
async function activeProjectIdentity(){
  const [projectResult,versionResult]=await Promise.allSettled([
    boundedProjectIdentity('/api/project',project=>{
      if(!project||typeof project.current!=='string'||!project.current.trim()||typeof project.dir!=='string'||!project.dir.trim())return null;
      return {name:project.current,key:project.dir,projects:Array.isArray(project.projects)?project.projects:[]};
    }),
    boundedProjectIdentity('/api/version',version=>{
      if(!version||typeof version.project!=='string'||!version.project.trim()||typeof version.projectDir!=='string'||!version.projectDir.trim())return null;
      return {name:version.project,key:version.projectDir,projects:[]};
    }),
  ]);
  if(projectResult.status==='fulfilled')return projectResult.value;
  if(versionResult.status==='fulfilled')return versionResult.value;
  throw new Error('active project unavailable');
}

export const projectStorageReady=activeProjectIdentity().then(identity=>setStorageProject(identity.key,identity.projects,identity.name));

let mapMod=null;
export function loadMapModule(){
  return projectStorageReady.then(()=>mapMod||(mapMod=import('./map.js')));
}
