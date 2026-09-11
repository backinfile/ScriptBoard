import atexit, hashlib, base64, json, os, pathlib, subprocess, sys, tempfile, shutil
spec = json.loads(base64.b64decode("__SPEC__"))
inputs = spec["inputs"]
config = spec["config"]
environment = config.get("environment", {})
if not isinstance(environment,dict) or any(not isinstance(k,str) or not isinstance(v,str) for k,v in environment.items()):
    raise ValueError("environment must be a JSON object of strings")
os.environ.update(environment)

def arg(name, default=None):
    return inputs.get(name, config.get(name, default))
def command(argv, capture=False):
    print("+ " + " ".join(str(x) for x in argv), flush=True)
    p = subprocess.run([str(x) for x in argv], check=True, text=True, stdout=subprocess.PIPE if capture else None)
    return p.stdout.strip() if capture else None
kind = spec["kind"]
result = {}
if kind == "custom":
    definition = spec["script"]
    language, code = definition["language"], definition["code"]
    if language == "python":
        scope = {"__name__": "__scriptboard_node__"}
        exec(compile(code, "<scriptboard-node>", "exec"), scope)
        result = scope["main"](inputs)
    else:
        with tempfile.TemporaryDirectory(prefix="scriptboard-node-") as temp:
            input_file = pathlib.Path(temp) / "input.json"
            output_file = pathlib.Path(temp) / "output.json"
            input_file.write_text(json.dumps(inputs), encoding="utf-8")
            env = os.environ.copy()
            env["SCRIPTBOARD_INPUT_FILE"] = str(input_file)
            env["SCRIPTBOARD_OUTPUT_FILE"] = str(output_file)
            if language == "javascript":
                script = pathlib.Path(temp) / "node.mjs"
                script.write_text(code, encoding="utf-8")
                bridge = pathlib.Path(temp) / "bridge.mjs"
                bridge.write_text('import fs from "node:fs"; import {main} from "./node.mjs"; const result=await main(JSON.parse(fs.readFileSync(process.env.SCRIPTBOARD_INPUT_FILE,"utf8")));fs.writeFileSync(process.env.SCRIPTBOARD_OUTPUT_FILE,JSON.stringify(result));', encoding="utf-8")
                argv = ["node", str(bridge)]
            else:
                script = pathlib.Path(temp) / ("node.ps1" if language == "powershell" else "node.sh")
                script.write_text(code, encoding="utf-8")
                argv = (["powershell" if os.name == "nt" else "pwsh", "-NoProfile", "-NonInteractive", "-File"] if language == "powershell" else ["sh"]) + [str(script)]
            subprocess.run(argv, env=env, check=True)
            result = json.loads(output_file.read_text(encoding="utf-8-sig"))
elif kind == "script":
    path = pathlib.Path(arg("script")).resolve()
    if hashlib.sha256(path.read_bytes()).hexdigest() != config["scriptDigest"]:
        raise ValueError("Local script changed before execution")
    extension = path.suffix.lower()
    prefix = {".py":[sys.executable], ".sh":["sh"], ".ps1":["powershell" if os.name=="nt" else "pwsh","-NoProfile","-File"], ".cmd":["cmd","/c"]}.get(extension)
    if not prefix:
        raise ValueError("Unsupported script extension")
    arguments = arg("arguments", [])
    if not isinstance(arguments,list):
        raise ValueError("arguments must be a JSON array")
    with tempfile.TemporaryDirectory(prefix="scriptboard-output-") as temp:
        env=os.environ.copy()
        env["SCRIPTBOARD_INPUT_FILE"]=str(pathlib.Path(temp)/"inputs.json")
        env["SCRIPTBOARD_OUTPUT_FILE"]=str(pathlib.Path(temp)/"outputs.json")
        pathlib.Path(env["SCRIPTBOARD_INPUT_FILE"]).write_text(json.dumps(inputs),encoding="utf-8")
        subprocess.run(prefix+[str(path)]+arguments,env=env,check=True)
        output=pathlib.Path(env["SCRIPTBOARD_OUTPUT_FILE"])
        result=json.loads(output.read_text(encoding="utf-8-sig")) if output.exists() else {}
elif kind == "git":
    repository, branch, directory = arg("repository"),arg("branch","dev"),arg("directory")
    git=["git"]
    if arg("skipTLSVerify",False): git += ["-c","http.sslVerify=false"]
    if not pathlib.Path(directory).exists():
        command(git+["clone","--branch",branch,"--",repository,directory])
    else:
        command(git+["-C",directory,"fetch","--",repository,branch])
        command(git+["-C",directory,"checkout","--detach","FETCH_HEAD"])
    result={"sourceDir":str(pathlib.Path(directory).resolve()),"sourceCommit":command(git+["-C",directory,"rev-parse","HEAD"],True)}
elif kind == "maven":
    goals=arg("goals",["clean","package"])
    if not isinstance(goals,list): raise ValueError("goals must be a JSON array")
    argv=[shutil.which("mvn") or "mvn","-B","-f",arg("pom","pom.xml")]
    if arg("profiles",""):argv+=["-P",arg("profiles")]
    if arg("skipTests",False):argv+=["-DskipTests"]
    command(argv+goals)
    artifacts=sorted(str(p.resolve()) for p in pathlib.Path("target").glob("*.jar"))
    if not artifacts:raise ValueError("Maven did not produce a JAR under target")
    result={"artifactPath":artifacts[0],"version":arg("version","")}
elif kind == "go":
    env=os.environ.copy()
    env["GOOS"]=arg("goos","linux");env["GOARCH"]=arg("goarch","amd64");env["CGO_ENABLED"]="1" if arg("cgo",False) else "0"
    binary=arg("binary","dist/server");pathlib.Path(binary).parent.mkdir(parents=True,exist_ok=True)
    argv=["go","build","-o",binary]
    if arg("ldflags",""):argv+=["-ldflags",arg("ldflags")]
    subprocess.run(argv+[arg("package","./cmd/server")],check=True,env=env)
    result={"binaryPath":str(pathlib.Path(binary).resolve()),"version":arg("version","")}
elif kind in ["image_build","image_push","image_pull"]:
    image=arg("image")
    docker=["docker"]
    if arg("dockerContext", ""):docker += ["--context",arg("dockerContext")]
    if arg("dockerConfig", ""):docker += ["--config",arg("dockerConfig")]
    if kind=="image_build":
        argv=docker+["build","-t",image,"-f",arg("dockerfile","Dockerfile"),"--platform",arg("platform","linux/amd64")]
        if not arg("cache",True):argv+=["--no-cache"]
        for key,value in arg("buildArgs",{}).items():argv+=["--build-arg",key+"="+str(value)]
        command(argv+[arg("context",".")])
    else:command(docker+["push" if kind=="image_push" else "pull",image])
    metadata=json.loads(command(docker+["image","inspect",image],True))[0]
    result={"imageRef":image,"imageId":metadata["Id"],"digest":(metadata.get("RepoDigests") or [""])[0]}
elif kind in ["k3d","k3s"]:
    kubectl=["kubectl"]
    if kind=="k3d":
        cluster=arg("cluster")
        if arg("importImage",False):command(["k3d","image","import",arg("image"),"-c",cluster])
        temp_config=tempfile.TemporaryDirectory(prefix="scriptboard-kube-")
        atexit.register(temp_config.cleanup)
        config_path=pathlib.Path(temp_config.name)/"config"
        config_path.write_text(command(["k3d","kubeconfig","get",cluster],True),encoding="utf-8")
        kubectl+=["--kubeconfig",str(config_path),"--context","k3d-"+cluster]
    else:
        kubectl+=["--kubeconfig",arg("kubeconfig")]
        if arg("context",""):kubectl+=["--context",arg("context")]
        if arg("server",""):kubectl+=["--server",arg("server")]
        if arg("skipTLSVerify",False):kubectl+=["--insecure-skip-tls-verify=true"]
    kubectl+=["-n",arg("namespace","default")]
    command(kubectl+["apply","-f",arg("manifest")])
    if arg("deployment","") and arg("image",""):
        command(kubectl+["set","image","deployment/"+arg("deployment"),arg("container","app")+"="+arg("image")])
    if arg("wait",True) and arg("deployment",""):
        command(kubectl+["rollout","status","deployment/"+arg("deployment"),"--timeout=300s"])
    result={"namespace":arg("namespace","default"),"endpoint":arg("endpoint","")}
else:raise ValueError("Unknown node type: "+kind)
if not isinstance(result,dict):raise ValueError("Node must return a JSON object")
pathlib.Path(spec["output"]).write_text(json.dumps(result,ensure_ascii=False),encoding="utf-8")
