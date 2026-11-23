"""Native host round trips using only stdlib and the already-built executable."""
import base64
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import zipfile
import zlib

HOST = str(Path(sys.argv[1]).resolve())
def run(action, kind, source, output, success=True):
    result = subprocess.run([HOST, action, kind, str(source), str(output)],
                            env={}, capture_output=True)
    assert (result.returncode == 0) == success, result.stderr.decode()
def dump(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False))
def assert_v1(path, kind):
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read("manifest.json"))
    assert type(manifest["package_version"]) is int and manifest["package_version"] == 1
    assert manifest["package_type"] == "starry_" + kind
def png(color):
    def chunk(kind, payload):
        return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", zlib.crc32(kind+payload))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 1, 1, 8, 2, 0, 0, 0)) +
            chunk(b"IDAT", zlib.compress(b"\x00"+bytes(color))) + chunk(b"IEND", b""))

with tempfile.TemporaryDirectory(prefix="starry-format-contract-") as temp:
    root = Path(temp)
    positions = ["before_char", "after_char", "before_author_note", "after_author_note",
                 "at_depth", "before_example", "after_example", "outlet"]
    entries = [{"id":i+30, "uid":f"foreign-{i}", "content":"龙🌟"*700, "keys":["术语"], "position":position,
                "role":2, "order":i, "outlet_name":"出口" if position=="outlet" else "",
                "extensions":{"st_extra":{"group":"组","group_override":1,"case_sensitive":True,
                  "scan_depth":6,"prevent_recursion":True,"automation_id":"task"}},
                "vectorized":True} for i, position in enumerate(positions)]
    source = root/"source.json"
    dump(source, {"name":"世界书", "scan_depth":5, "entries":entries})
    inspection = root/"inspect"
    inspection.mkdir()
    run("inspect","worldbook",source,inspection)
    graph = json.loads((inspection/"graph.json").read_text())
    converted = root/"export.json"
    graph["character"] = None
    graphfile = root/"graph.json"
    dump(graphfile,graph)
    run("export","worldbook",graphfile,converted)
    again = root/"again"
    again.mkdir()
    run("inspect","worldbook",converted,again)
    actual = json.loads((again/"graph.json").read_text())["worldbooks"][0]["entries"]
    assert len(actual)==8
    assert [e["position"] for e in actual] == positions
    assert [e["id"] for e in actual] == list(range(30,38))
    assert all(e["content"]=="龙🌟"*700 and e["vectorized"] is True for e in actual)
    assert all(e["extensions"]["st_extra"]["group_override"]==1 for e in actual)
    print("PASS worldbook JSON: numeric IDs, eight positions, advanced fields, Unicode and vectorized")

    card = {"id":"c","name":"角色","nickname":"nickname","description":"龙🌟"*800,
            "first_message":"你好","example_messages":"example","system_prompt":"system",
            "post_history_instructions":"history","talkativeness":0.73,"depth_prompt":{"text":"depth","depth":7,"role":"user"},
            "alternate_greetings":["一","二"],"character_version":"v"}
    graph["character"] = card
    graph["worldbooks"].append({**graph["worldbooks"][0],"id":"b2","name":"第二本"})
    books=graph["worldbooks"]
    graph["bindings"]=[{"worldbook_id":books[0]["id"],"reference_type":"embedded"},
                       {"worldbook_id":"b2","reference_type":"reference"}]
    graph["relations"]=[]
    graph["media"]=[]
    for i, role in enumerate(["avatar","cover","illustration","background"]):
        content=png([i*40,30,160])
        digest=hashlib.sha256(content).hexdigest()
        path=root/digest
        path.write_bytes(content)
        graph["media"].append({"sha256":digest,"file":str(path),"mediaType":"image/png"})
        graph["relations"].append({"role":role,"target_type":"asset","target_id":digest})
    dump(graphfile,graph)
    package = root/"full.starry"
    run("export","character",graphfile,package)
    assert_v1(package, "character")
    unpacked=root/"unpacked"
    unpacked.mkdir()
    run("inspect","character",package,unpacked)
    actual=json.loads((unpacked/"graph.json").read_text())
    assert len(actual["worldbooks"])==2 and len(actual["bindings"])==2
    assert [b["reference_type"] for b in actual["bindings"]]==["embedded","reference"]
    assert len(actual["media"])==4 and len(actual["relations"])==4
    assert actual["character"]["description"]==card["description"]
    assert actual["character"]["depth_prompt"]==card["depth_prompt"]
    assert actual["character"]["character_version"]==card["character_version"]
    expected_media = set()
    for item, role in zip(graph["media"], ["avatar","cover","illustration","background"]):
        prepared = root / ("expected-"+role)
        run("image", role, item["file"], prepared)
        expected_media.add(hashlib.sha256(prepared.read_bytes()).hexdigest())
        assert prepared.read_bytes().startswith(b"RIFF") and prepared.read_bytes()[8:12] == b"WEBP"
        repeated = root / ("repeated-"+role)
        run("image", role, prepared, repeated)
        assert prepared.read_bytes() == repeated.read_bytes(), "canonical images must be reused"
    assert {m["sha256"] for m in actual["media"]} == expected_media
    assert {r["target_id"] for r in actual["relations"]} == expected_media
    assert all(hashlib.sha256(Path(m["file"]).read_bytes()).hexdigest() == m["sha256"] for m in actual["media"])
    assert all(e["extensions"]["starry_file_source"]["vectorized"] is True
               for e in actual["worldbooks"][0]["entries"])
    print("PASS .starry: two books, four slots, canonical WebP hashes, consistent references and byte reuse")
    run("export","character",graphfile,root/"incomplete.json",False)
    assert not (root/"incomplete.json").exists()
    print("PASS incompatible format rejected without partial output")

    # Formats carrying only an avatar and one embedded book.
    graph["worldbooks"]=books[:1]
    graph["bindings"]=graph["bindings"][:1]
    graph["media"]=graph["media"][:1]
    graph["relations"]=graph["relations"][:1]
    dump(graphfile,graph)
    for ext in ("png","charx"):
        output=root/f"card.{ext}"
        run("export","character",graphfile,output)
        stage=root/ext
        stage.mkdir()
        run("inspect","character",output,stage)
        actual=json.loads((stage/"graph.json").read_text())
        assert len(actual["worldbooks"])==1 and len(actual["media"])==1
        assert actual["character"]["first_message"]==card["first_message"]
        assert actual["character"]["depth_prompt"]==card["depth_prompt"]
        assert len(actual["worldbooks"][0]["entries"])==8
        print(f"PASS {ext}: avatar, embedded book and character fields")
    graph["media"]=[]
    graph["relations"]=[]
    dump(graphfile,graph)
    output=root/"card.json"
    run("export","character",graphfile,output)
    stage=root/"json"
    stage.mkdir()
    run("inspect","character",output,stage)
    assert len(json.loads((stage/"graph.json").read_text())["worldbooks"])==1
    print("PASS character JSON embedded book")

    graph["worldbooks"][0]["entries"]=[]
    graph["worldbooks"][0]["name"]="空条目书"
    dump(graphfile,graph)
    for ext in ("json","png","charx"):
        output=root/f"empty-book.{ext}"
        run("export","character",graphfile,output)
        stage=root/("empty-"+ext)
        stage.mkdir()
        run("inspect","character",output,stage)
        actual=json.loads((stage/"graph.json").read_text())
        assert len(actual["worldbooks"])==1 and len(actual["bindings"])==1
        assert actual["worldbooks"][0]["entries"]==[]
        assert actual["worldbooks"][0]["name"]=="空条目书"
        assert actual["bindings"][0]["reference_type"]=="embedded"
        print(f"PASS {ext}: explicit empty embedded worldbook retained")

    skill_doc = "---\nname: lawine-role\ndescription: Preserve character judgment\n---\n# 演绎\n保持观察与判断。\n"
    skill = {"id":"skill-1","display_name":"拉薇演绎","summary":"行为逻辑",
             "scope":"character_bound","tags":["role"],"eager_paths":["references/core.md"],
             "required_tools":[],"annotations":{},"files":[
                 {"path":"SKILL.md","content":base64.b64encode(skill_doc.encode()).decode(),"media_type":"text/markdown"},
                 {"path":"references/core.md","content":base64.b64encode("人物行为🌟".encode()).decode(),"media_type":"text/markdown"},
                 {"path":"assets/sample.bin","content":base64.b64encode(bytes(range(256))).decode(),"media_type":"application/octet-stream"}]}
    origin = {"owner_user_id":"account","resource_type":"skill",
              "resource_id":"12345678-1234-4234-8234-123456789abc"}
    skill_graph = {"skills":[skill],"worldbooks":[],"media":[],"acquisition_origins":{"skill-1":origin}}
    dump(graphfile,skill_graph)
    skill_package = root/"skill.starry"
    run("export","skill",graphfile,skill_package)
    assert_v1(skill_package, "skill")
    stage=root/"skill-stage"
    stage.mkdir()
    run("inspect","skill",skill_package,stage)
    actual=json.loads((stage/"graph.json").read_text())
    assert actual["skills"]==[skill] and actual["acquisition_origins"]=={"skill-1":origin}
    renamed = root/"skill.zip"
    renamed.write_bytes(skill_package.read_bytes())
    stage=root/"skill-zip-stage"
    stage.mkdir()
    run("inspect","skill",renamed,stage)
    actual=json.loads((stage/"graph.json").read_text())
    assert actual["skills"]==[skill] and actual["acquisition_origins"]=={"skill-1":origin}
    source=root/"SKILL.md"
    source.write_text(skill_doc)
    stage=root/"markdown-stage"
    stage.mkdir()
    run("inspect","skill",source,stage)
    assert json.loads((stage/"graph.json").read_text())["skills"][0]["display_name"]=="lawine-role"
    print("PASS Skill: standalone Markdown and .starry, text/binary tree and working origin")

    graph["skills"]=[skill]
    graph["skill_bindings"]=[{"id":"binding-skill","character_id":"c","skill_id":"skill-1",
                             "skill_snapshot":{key:value for key,value in skill.items() if key!="id"},
                             "recommended_config":{},
                             "enabled":False,"activation":"manual","required":True,"sort_order":7,"purpose":"演绎"}]
    graph["acquisition_origins"]={"skill-1":origin}
    dump(graphfile,graph)
    role_package=root/"role.starry"
    run("export","character",graphfile,role_package)
    assert_v1(role_package, "character")
    stage=root/"role-stage"
    stage.mkdir()
    run("inspect","character",role_package,stage)
    actual=json.loads((stage/"graph.json").read_text())
    assert actual["skills"]==[skill] and actual["skill_bindings"]==graph["skill_bindings"]
    assert actual["acquisition_origins"]=={"skill-1":origin}
    run("export","character",graphfile,root/"lossy-role.json",False)
    assert not (root/"lossy-role.json").exists()
    print("PASS character Skill: disabled/manual binding preserved and lossy formats rejected")

    graph["characters"]=[card,{**card,"id":"c2","name":"第二角色"}]
    graph["character"]=None
    graph["bindings"][0]["character_id"]="c"
    graph["collection"]={"id":"set","name":"演绎合集","description":"角色与独立 Skill","tags":["角色"]}
    graph["collection_items"]=[
        {"id":"item-1","collection_id":"set","target_type":"character","target_id":"c","sort_order":0,"group":"角色","note":"完整绑定"},
        {"id":"item-2","collection_id":"set","target_type":"character","target_id":"c2","sort_order":1,"group":"角色","note":""},
        {"id":"item-3","collection_id":"set","target_type":"skill","target_id":"skill-1","sort_order":2,"group":"演绎","note":"共享 Skill"}]
    dump(graphfile,graph)
    set_package=root/"collection.starry"
    run("export","collection",graphfile,set_package)
    assert_v1(set_package, "collection")
    stage=root/"collection-stage"
    stage.mkdir()
    run("inspect","collection",set_package,stage)
    actual=json.loads((stage/"graph.json").read_text())
    assert len(actual["characters"])==2 and actual["skills"]==[skill]
    assert actual["collection_items"]==graph["collection_items"]
    assert actual["skill_bindings"]==graph["skill_bindings"]
    assert actual["acquisition_origins"]=={"skill-1":origin}
    print("PASS collection: shared Skill, member ordering/groups/notes and typed role bindings")

    # Valid packages may carry configuration the four-slot machine API cannot
    # preserve. Reject it before emitting an import plan.
    def alter_package(source, output, path, edit):
        with zipfile.ZipFile(source) as archive:
            files = {name:archive.read(name) for name in archive.namelist()}
        value=json.loads(files[path])
        edit(value)
        files[path]=json.dumps(value).encode()
        with zipfile.ZipFile(output,"w") as archive:
            for name,value in files.items():
                archive.writestr(name,value)
    for kind, package in (("character", role_package), ("skill", skill_package),
                          ("collection", set_package)):
        for index, version in enumerate((2, 3, "1", 1.5)):
            changed=root/f"invalid-{kind}-{index}.starry"
            alter_package(package, changed, "manifest.json",
                          lambda data: data.update(package_version=version))
            stage=root/f"invalid-{kind}-{index}"
            stage.mkdir()
            run("inspect", kind, changed, stage, False)
            assert not (stage/"graph.json").exists()
    print("PASS V1 format: character, Skill and collection reject other versions and invalid types")
    with zipfile.ZipFile(role_package) as archive:
        preset_path=next(name for name in archive.namelist() if name.endswith("/preset.json"))
    default_package=root/"forge-default.starry"
    alter_package(role_package,default_package,preset_path,
                  lambda data: data.update({"name":"Default","slug":"default"}))
    default_stage=root/"forge-default-stage"
    default_stage.mkdir()
    run("inspect","character",default_package,default_stage)
    assert json.loads((default_stage/"graph.json").read_text())["character"]["name"] == card["name"]
    for field,value in (("description","Alternate presentation"),("name","Different preset"),
                        ("is_archived",True),("is_default",False)):
        changed=root/f"preset-{field}.starry"
        alter_package(role_package,changed,preset_path,lambda data: data.update({field:value}))
        stage=root/("preset-stage-"+field)
        stage.mkdir()
        run("inspect","character",changed,stage,False)
        assert not (stage/"graph.json").exists()
    print("PASS meaningful preset metadata rejected before import planning")
