#!/usr/bin/env python3
"""Regenerate the per-domain MCP tool tables of docs.signal18.io (pages/03.usage/07.mcp)
from the tool definitions in mcp/tools_*.go, so names and parameters never drift.
Usage: doc/implementation/mcp/gen_tool_tables.py <path to docs.md> ; replaces everything
after the first "**Cluster**" heading of section 3.8.6."""
import re, glob, sys
tools=[]
for f in sorted(glob.glob('mcp/tools_*.go')):
    s=open(f).read()
    for m in re.finditer(r'mcp\.NewTool\("([a-z0-9-]+)",\s*mcp\.WithDescription\("((?:[^"\\]|\\.)*)"\)(.*?)\n\t\t\),', s, re.S):
        name, desc, rest = m.group(1), m.group(2), m.group(3)
        params=[]
        for pm in re.finditer(r'mcp\.With(String|Number|Boolean|Array)\("([a-z_]+)"([^\n]*)', rest):
            params.append('`'+pm.group(2)+('`\\*' if 'Required()' in pm.group(3) else '`'))
        first=re.split(r'(?<=[.!:])\s', desc.replace('\\"','"'))[0].rstrip(':.').replace('|','/')
        tools.append((f, name, params, first))
def domain(f,n):
    if 'cloud18' in f: return 'Cloud18'
    if 'database' in n: return 'Database servers'
    if n.startswith(('list-proxies','get-proxy','proxy-')): return 'Proxies'
    return 'Cluster'
def kind(n): return 'read' if n.startswith(('list-','get-','check-')) else 'action'
order=['Cluster','Database servers','Proxies','Cloud18']
groups={d:[] for d in order}
for t in tools: groups[domain(t[0],t[1])].append(t)
md=''
for d in order:
    rows=sorted(groups[d], key=lambda t:(kind(t[1])!='read', t[1]))
    md+=f'\n**{d}**\n\n| Tool | Kind | Parameters | What it does |\n| --- | --- | --- | --- |\n'
    for f,n,p,desc in rows:
        md+=f'| `{n}` | {kind(n)} | {", ".join(p)} | {desc} |\n'
md+='\n\\* required parameter. Reads need the matching `*-show` grant, actions the grant of their REST route (for example `cluster-switchover`, `db-restore`, `cluster-bench`); every tool is one route, see the ACL table in the server.\n'
D=sys.argv[1]
s=open(D).read()
i=s.index('**Cluster**')
open(D,'w').write(s[:i].rstrip('\n')+'\n'+md)
print(len(tools), {d:len(groups[d]) for d in order})
