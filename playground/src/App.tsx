import React, { useState } from 'react';
import Editor from '@monaco-editor/react';

const LANGUAGES: Record<string, { label: string; defaultCode: string; hasStdin?: boolean; monaco: string }> = {
  py3: { label: 'Python 3', defaultCode: 'print("hello from python 3")', hasStdin: true, monaco: 'python' },
  c: { label: 'C', defaultCode: '#include <stdio.h>\nint main() {\n  printf("hello from c\\n");\n  return 0;\n}', monaco: 'c' },
  cpp: { label: 'C++', defaultCode: '#include <iostream>\nint main() {\n  std::cout << "hello from c++" << std::endl;\n  return 0;\n}', monaco: 'cpp' },
  java: { label: 'Java', defaultCode: 'public class Main {\n  public static void main(String[] args) {\n    System.out.println("hello from java");\n  }\n}', monaco: 'java' },
  bash: { label: 'Bash', defaultCode: 'echo "hello from bash"', monaco: 'shell' },
  js: { label: 'JavaScript (Node)', defaultCode: 'console.log("hello from node");', hasStdin: true, monaco: 'javascript' },
  verilog: { label: 'Verilog', defaultCode: 'module hello;\ninitial begin\n  $display("hello from verilog");\n  $finish;\nend\nendmodule', monaco: 'verilog' },
  rust: { label: 'Rust', defaultCode: 'fn main() {\n  println!("hello from rust");\n}', monaco: 'rust' },
  go: { label: 'Go', defaultCode: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello from go")\n}', monaco: 'go' },
};

interface Preset { source: string; stdin?: string; expected?: string; label: string }
type PresetMap = Record<string, Preset[]>;

const PRESETS: PresetMap = {
  py3: [
    // basics
    { label: 'hello world', source: 'print("hello world")', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'def fib(n):\n  a, b = 0, 1\n  for _ in range(n):\n    print(a)\n    a, b = b, a+b\n\nfib(10)', stdin: '', expected: '' },
    // bad
    { label: '[bad] infinite loop', source: 'import time\nwhile True:\n  time.sleep(1)' },
    { label: '[bad] memory bomb', source: 'l = []\nwhile True:\n  l.append("a" * 1024 * 1024)' },
    { label: '[bad] recursion depth', source: 'def f():\n  f()\nf()' },
    // leetcode
    { label: '[lc easy] two sum', source: 'def two_sum(nums, target):\n  seen = {}\n  for i, v in enumerate(nums):\n    need = target - v\n    if need in seen:\n      return [seen[need], i]\n    seen[v] = i\n  return []\n\nprint(two_sum([2,7,11,15], 9))' },
    { label: '[lc easy] fizzbuzz', source: 'def fizzbuzz(n):\n  for i in range(1, n+1):\n    if i % 15 == 0: print("FizzBuzz")\n    elif i % 3 == 0: print("Fizz")\n    elif i % 5 == 0: print("Buzz")\n    else: print(i)\n\nfizzbuzz(15)' },
    { label: '[lc medium] reverse linked list', source: 'class ListNode:\n  def __init__(self, val=0, next=None):\n    self.val = val\n    self.next = next\n\ndef reverseList(head):\n  prev = None\n  curr = head\n  while curr:\n    nxt = curr.next\n    curr.next = prev\n    prev = curr\n    curr = nxt\n  return prev\n\n# build 1->2->3->None\nhead = ListNode(1, ListNode(2, ListNode(3)))\nresult = reverseList(head)\nwhile result:\n  print(result.val, end=" ")\n  result = result.next\nprint()' },
    { label: '[lc medium] valid parentheses', source: 'def isValid(s):\n  pairs = {")":"(", "]":"[", "}":"{"}\n  stack = []\n  for c in s:\n    if c in pairs:\n      if not stack or stack.pop() != pairs[c]:\n        return False\n    else:\n      stack.append(c)\n  return len(stack) == 0\n\nprint(isValid("()[]{}"))\nprint(isValid("([)]"))\nprint(isValid("{[]}"))' },
  ],
  c: [
    { label: 'hello world', source: '#include <stdio.h>\nint main() {\n  printf("hello world\\n");\n  return 0;\n}', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: '#include <stdio.h>\nint fib(int n) {\n  if (n <= 1) return n;\n  return fib(n-1) + fib(n-2);\n}\nint main() {\n  for (int i = 0; i < 10; i++)\n    printf("%d\\n", fib(i));\n  return 0;\n}' },
    { label: '[bad] segfault', source: '#include <stdio.h>\nint main() {\n  int *p = NULL;\n  *p = 42;\n  printf("this wont print\\n");\n  return 0;\n}' },
    { label: '[bad] infinite loop', source: '#include <stdio.h>\nint main() {\n  for(;;);\n  return 0;\n}' },
    { label: '[bad] stack overflow', source: 'void f() { f(); }\nint main() { f(); return 0; }' },
    { label: '[bad] buffer overflow', source: '#include <stdio.h>\n#include <string.h>\nint main() {\n  char buf[4];\n  strcpy(buf, "this is way too long");\n  printf("%s\\n", buf);\n  return 0;\n}' },
  ],
  cpp: [
    { label: 'hello world', source: '#include <iostream>\nint main() {\n  std::cout << "hello world" << std::endl;\n  return 0;\n}', stdin: '', expected: 'hello world\n' },
    { label: '[bad] segfault', source: '#include <iostream>\nint main() {\n  int* p = nullptr;\n  *p = 42;\n  std::cout << "crash" << std::endl;\n  return 0;\n}' },
    { label: '[bad] infinite loop', source: '#include <iostream>\nint main() {\n  while(true) {}\n  return 0;\n}' },
    { label: '[bad] abort', source: '#include <cstdlib>\nint main() {\n  abort();\n  return 0;\n}' },
  ],
  java: [
    { label: 'hello world', source: 'public class Main {\n  public static void main(String[] args) {\n    System.out.println("hello world");\n  }\n}', stdin: '', expected: 'hello world\n' },
    { label: '[bad] null pointer', source: 'public class Main {\n  public static void main(String[] args) {\n    Object o = null;\n    o.toString();\n  }\n}' },
    { label: '[bad] infinite loop', source: 'public class Main {\n  public static void main(String[] args) {\n    while(true) {}\n  }\n}' },
    { label: '[bad] stack overflow', source: 'public class Main {\n  static void f() { f(); }\n  public static void main(String[] args) {\n    f();\n  }\n}' },
  ],
  bash: [
    { label: 'hello world', source: 'echo "hello world"', stdin: '', expected: 'hello world\n' },
    { label: 'count', source: 'for i in 1 2 3; do\necho "line $i"\ndone', stdin: '', expected: 'line 1\nline 2\nline 3\n' },
    { label: '[bad] fork bomb', source: 'f() { f|f & }; f' },
  ],
  js: [
    { label: 'hello world', source: 'console.log("hello world");', stdin: '', expected: 'hello world\n' },
    { label: '[bad] infinite loop', source: 'while(true) {}' },
  ],
  verilog: [
    { label: 'hello world', source: 'module hello;\ninitial begin\n  $display("hello world");\n  $finish;\nend\nendmodule', stdin: '', expected: 'hello world\n' },
  ],
  rust: [
    { label: 'hello world', source: 'fn main() {\n  println!("hello world");\n}', stdin: '', expected: 'hello world\n' },
    { label: '[bad] panic', source: 'fn main() {\n  panic!("crash");\n}' },
  ],
  go: [
    { label: 'hello world', source: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello world")\n}', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'package main\nimport "fmt"\nfunc fib(n int) int {\n  if n <= 1 { return n }\n  return fib(n-1) + fib(n-2)\n}\nfunc main() {\n  for i := 0; i < 10; i++ {\n    fmt.Println(fib(i))\n  }\n}' },
    { label: '[bad] nil deref', source: 'package main\nfunc main() {\n  var p *int\n  *p = 42\n}' },
  ],
};

const API = window.location.origin;

function flattenPresets(ps: PresetMap, lang: string): { name: string; preset: Preset }[] {
  const result: { name: string; preset: Preset }[] = [];
  for (const p of ps[lang] || []) {
    let name = p.label;
    let idx = 1;
    while (result.some(r => r.name === name)) name = p.label + ' (' + (++idx) + ')';
    result.push({ name, preset: p });
  }
  return result;
}

function App() {
  const [lang, setLang] = useState('py3');
  const [code, setCode] = useState(LANGUAGES['py3'].defaultCode);
  const [stdin, setStdin] = useState('');
  const [output, setOutput] = useState('');
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState<'play' | 'bench'>('play');
  const LI = LANGUAGES[lang];

  function switchLang(newLang: string) {
    setLang(newLang);
    setCode(LANGUAGES[newLang].defaultCode);
    setStdin('');
    setOutput('');
  }

  function loadPreset(p: Preset) {
    setCode(p.source);
    if (p.stdin !== undefined) setStdin(p.stdin);
    setOutput('');
  }

  async function run() {
    setLoading(true);
    setOutput('');
    const body: any = { language: lang, source: code, tests: [{ stdin: stdin || '', expected_stdout: '' }] };
    try {
      const res = await fetch(API + '/run', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      const data = await res.json();
      if (data.error) { setOutput('error: ' + data.error.message); }
      else {
        let out = 'status: ' + data.status + '\n';
        if (data.build) out += 'build: ' + data.build.status + ' (' + data.build.duration_ms + 'ms)\n';
        if (data.tests && data.tests.length > 0) {
          const t = data.tests[0];
          out += 'test status: ' + t.status + '\n';
          out += '---stdout---\n' + t.stdout;
          if (t.stderr) out += '\n---stderr---\n' + t.stderr;
          if (t.memory_peak_kb > 0) out += '\nmemory: ' + t.memory_peak_kb + ' KB';
        }
        setOutput(out);
      }
    } catch (e: any) { setOutput('request failed: ' + e.message); }
    setLoading(false);
  }

  const flatPresets = flattenPresets(PRESETS, lang);

  if (page === 'bench') {
    return React.createElement('div', { style: { fontFamily: 'monospace', padding: '10px', maxWidth: '1000px', margin: '0 auto' } },
      React.createElement('h1', null, 'benchmarks'),
      React.createElement('button', { onClick: () => setPage('play'), style: { marginBottom: '10px', cursor: 'pointer' } }, 'back to playground'),
      React.createElement('iframe', { src: API + '/playground/', style: { width: '100%', height: '600px', border: '1px solid #ccc' } }),
    );
  }

  return React.createElement('div', { style: { fontFamily: 'monospace', padding: '10px', maxWidth: '1400px', margin: '0 auto' } },
    React.createElement('div', { style: { display: 'flex', gap: '20px', alignItems: 'center', marginBottom: '10px' } },
      React.createElement('h1', { style: { margin: 0 } }, 'goboxd'),
      React.createElement('a', { href: '#', onClick: (e: any) => { e.preventDefault(); setPage('bench'); }, style: { fontSize: '14px' } }, 'benchmarks'),
      React.createElement('a', { href: API + '/info', target: '_blank', style: { fontSize: '14px' } }, '/info'),
      React.createElement('a', { href: API + '/readyz', target: '_blank', style: { fontSize: '14px' } }, '/readyz'),
    ),
    React.createElement('div', { style: { marginBottom: '10px', display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' } },
      React.createElement('label', null, 'language: '),
      React.createElement('select', { value: lang, onChange: (e: any) => switchLang(e.target.value), style: { fontSize: '14px', padding: '4px' } },
        Object.entries(LANGUAGES).map(([id, l]) => React.createElement('option', { key: id, value: id }, l.label))
      ),
      React.createElement('label', null, 'preset: '),
      React.createElement('select', { onChange: (e: any) => { const p = flatPresets.find(x => x.name === e.target.value); if (p) loadPreset(p.preset); }, defaultValue: '', style: { fontSize: '14px', padding: '4px', minWidth: '250px' } },
        React.createElement('option', { value: '', disabled: true }, 'select preset...'),
        flatPresets.map(({ name }) => React.createElement('option', { key: name, value: name }, name))
      ),
    ),
    React.createElement('div', { style: { display: 'flex', gap: '10px', height: '550px' } },
      React.createElement('div', { style: { flex: 2, display: 'flex', flexDirection: 'column' } },
        React.createElement(Editor, {
          height: '100%',
          language: LI.monaco,
          value: code,
          onChange: (v: string | undefined) => setCode(v || ''),
          theme: 'vs-dark',
          options: { minimap: { enabled: false }, fontSize: 14, lineNumbers: 'on', scrollBeyondLastLine: false },
        }),
      ),
      React.createElement('div', { style: { flex: 1, display: 'flex', flexDirection: 'column', gap: '10px' } },
        LI.hasStdin && React.createElement('div', { style: { display: 'flex', flexDirection: 'column', gap: '5px' } },
          React.createElement('label', null, 'stdin:'),
          React.createElement('textarea', { value: stdin, onChange: (e: any) => setStdin(e.target.value), rows: 3, style: { fontFamily: 'monospace', fontSize: '13px' } }),
        ),
        React.createElement('button', {
          onClick: run, disabled: loading,
          style: { padding: '10px', fontSize: '16px', cursor: loading ? 'wait' : 'pointer', background: loading ? '#ccc' : '#4CAF50', color: 'white', border: 'none', borderRadius: '4px' }
        }, loading ? 'running...' : 'run'),
        React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '13px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px', minHeight: '200px' } },
          output || 'output will appear here'),
      ),
    ),
  );
}

export default App;
