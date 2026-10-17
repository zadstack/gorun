// Three.js r180, self-hosted. The semantic hero and static shape never depend on WebGL.
const motion = matchMedia("(prefers-reduced-motion: reduce)");
const scenes = [...document.querySelectorAll(".gp-hero .gp-hero-scene")].slice(0, 2);
const cleanups = new Set();
let suspended = false;
if (!motion.matches && !navigator.connection?.saveData) {
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      observer.unobserve(entry.target);
      mount(entry.target).catch(() => {}); // Keep the CSS fallback on unsupported devices.
    }
  }, {rootMargin: "100px"});
  scenes.forEach(element => observer.observe(element));
  addEventListener("pagehide", () => { suspended = true; observer.disconnect(); cleanups.forEach(fn => fn()); });
  addEventListener("pageshow", event => {
    suspended = false;
    if (event.persisted && !motion.matches) scenes.forEach(element => observer.observe(element));
  });
}
async function mount(element) {
  const T = await import("./three.module.min.js");
  if (!element.isConnected || motion.matches || suspended) return;
  const renderer = new T.WebGLRenderer({alpha: true, antialias: true, powerPreference: "low-power"});
  renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 1.5));
  renderer.domElement.setAttribute("aria-hidden", "true");
  const scene = new T.Scene();
  const camera = new T.PerspectiveCamera(36, 1, 0.1, 30);
  camera.position.z = 7;
  const group = new T.Group();
  group.rotation.set(.3, -.35, .15);
  scene.add(group);
  const color = getComputedStyle(element).color;
  const material = new T.MeshStandardMaterial({color, roughness: .38, metalness: .15});
  const geometries = [];
  const add = (geometry, x, y, z, rotation = 0) => {
    geometries.push(geometry);
    const mesh = new T.Mesh(geometry, material);
    mesh.position.set(x,y,z); mesh.rotation.z = rotation; group.add(mesh);
  };
  if (element.classList.contains("gp-scene-blocks")) {
    for(let i=0;i<5;i++) add(new T.BoxGeometry(.82,.82,.82), (i%3-1)*1.05, (Math.floor(i/3)-.5)*1.05, (i%2)*.3, i*.12);
  } else if (element.classList.contains("gp-scene-ribbons")) {
    for(let i=0;i<3;i++) add(new T.TorusGeometry(1+i*.16,.10,10,64,Math.PI*1.65),0,0,i*.22,(i-1)*.65);
  } else {
    for(let i=0;i<3;i++) {
      add(new T.TorusGeometry(1.25,.045,8,64),0,0,0);
      group.children[i].rotation.x = i*Math.PI/3;
    }
    add(new T.IcosahedronGeometry(.55,1),0,0,0);
  }
  scene.add(new T.HemisphereLight(0xffffff,0x596575,3));
  const light = new T.DirectionalLight(0xffffff,3);
  light.position.set(3,5,5); scene.add(light);
  let disposed = false, visible = true, frame = 0;
  function resize() {
    const {width,height} = element.getBoundingClientRect();
    if (!width || !height) return;
    camera.aspect = width/height; camera.updateProjectionMatrix();
    renderer.setSize(width,height,false);
  }
  function dispose() {
    if(disposed) return;
    disposed=true; cancelAnimationFrame(frame); resizeObserver.disconnect(); visibility.disconnect();
    document.removeEventListener("visibilitychange", resume);
    motion.removeEventListener("change", motionChange);
    geometries.forEach(g=>g.dispose()); material.dispose(); renderer.dispose();
    renderer.domElement.remove(); element.classList.remove("gp-scene-ready");
    cleanups.delete(dispose);
  }
  function draw(time) {
    frame=0;
    if(!element.isConnected) {dispose();return;}
    if(disposed || !visible || document.hidden) return;
    group.rotation.y = -.35 + Math.sin(time*.00014)*.3;
    renderer.render(scene,camera); frame=requestAnimationFrame(draw);
  }
  function resume() {if(!frame && !disposed && visible && !document.hidden) frame=requestAnimationFrame(draw);}
  function motionChange(){if(motion.matches) dispose();}
  const resizeObserver = new ResizeObserver(resize);
  const visibility = new IntersectionObserver(entries=>{visible=entries[0].isIntersecting;resume();});
  resizeObserver.observe(element); visibility.observe(element);
  document.addEventListener("visibilitychange",resume); motion.addEventListener("change",motionChange);
  renderer.domElement.addEventListener("webglcontextlost",dispose,{once:true});
  element.append(renderer.domElement); resize();
  try {renderer.render(scene,camera);element.classList.add("gp-scene-ready");cleanups.add(dispose);resume();}
  catch(error) {dispose();throw error;}
}
