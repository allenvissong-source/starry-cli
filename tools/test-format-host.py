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
    unpacked=root/"unpacked"
    unpacked.mkdir()
    run("inspect","character",package,unpacked)
    actual=json.loads((unpacked/"graph.json").read_text())
    assert len(actual["worldbooks"])==2 and len(actual["bindings"])==2
    assert [b["reference_type"] for b in actual["bindings"]]==["embedded","reference"]
    assert len(actual["media"])==4 and len(actual["relations"])==4
    assert actual["character"]["description"]==card["description"]
    assert actual["character"]["depth_prompt"]==card["depth_prompt"]
    assert {m["sha256"] for m in actual["media"]}=={m["sha256"] for m in graph["media"]}
    assert all(e["extensions"]["starry_file_source"]["vectorized"] is True
               for e in actual["worldbooks"][0]["entries"])
    print("PASS .starry: two books, reference/embedded, four slots, verified image bytes and prompts")
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
