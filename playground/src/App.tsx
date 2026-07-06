import React, { useState } from 'react';
import Editor from '@monaco-editor/react';

const LANGUAGES: Record<string, { label: string; defaultCode: string; hasStdin?: boolean; monaco: string }> = {
  bash: { label: 'Bash', defaultCode: 'echo "hello from bash"', monaco: 'shell' },
  c: { label: 'C', defaultCode: '#include <stdio.h>\nint main() {\n  printf("hello from c\\n");\n  return 0;\n}', monaco: 'c' },
  cpp: { label: 'C++', defaultCode: '#include <iostream>\nint main() {\n  std::cout << "hello from c++" << std::endl;\n  return 0;\n}', monaco: 'cpp' },
  d: { label: 'D (GDC)', defaultCode: 'import std.stdio;\nvoid main() {\n  writeln("hello from d");\n}', monaco: 'cpp' },
  erl: { label: 'Erlang', defaultCode: '-module(solution).\n-export([start/0]).\nstart() ->\n  io:format("hello from erlang~n").', monaco: 'erlang' },
  go: { label: 'Go', defaultCode: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello from go")\n}', monaco: 'go' },
  haskell: { label: 'Haskell', defaultCode: 'main = putStrLn "hello from haskell"', monaco: 'haskell' },
  java: { label: 'Java', defaultCode: 'public class Main {\n  public static void main(String[] args) {\n    System.out.println("hello from java");\n  }\n}', monaco: 'java' },
  js: { label: 'JavaScript (Node)', defaultCode: 'console.log("hello from node");', hasStdin: true, monaco: 'javascript' },
  lisp: { label: 'Lisp (SBCL)', defaultCode: '(format t "hello from lisp~%")', monaco: 'lisp' },
  lua: { label: 'Lua (LuaJIT)', defaultCode: 'print("hello from lua")', monaco: 'lua' },
  ocaml: { label: 'OCaml', defaultCode: 'print_string "hello from ocaml\\n"', monaco: 'ocaml' },
  perl: { label: 'Perl', defaultCode: 'print "hello from perl\\n"', monaco: 'perl' },
  py3: { label: 'Python 3', defaultCode: 'print("hello from python 3")', hasStdin: true, monaco: 'python' },
  r: { label: 'R', defaultCode: 'cat("hello from r\\n")', hasStdin: true, monaco: 'r' },
  rust: { label: 'Rust', defaultCode: 'fn main() {\n  println!("hello from rust");\n}', monaco: 'rust' },
  verilog: { label: 'Verilog', defaultCode: 'module hello;\ninitial begin\n  $display("hello from verilog");\n  $finish;\nend\nendmodule', monaco: 'verilog' },
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
    { label: 'MemoryHog 10MB', source: 'public class Main {\n    public static void main(String[] args) throws InterruptedException {\n        final int megabytes = 10;\n        final int blockSize = 1 << 20;\n        final byte[][] blocks = new byte[megabytes][];\n\n        long checksum = 0;\n\n        for (int i = 0; i < megabytes; i++) {\n            byte[] block = new byte[blockSize];\n            for (int j = 0; j < blockSize; j += 4096) {\n                block[j] = (byte) (i * 31 + j);\n                checksum += block[j];\n            }\n            blocks[i] = block;\n        }\n\n        for (byte[] block : blocks) {\n            for (int j = 0; j < block.length; j += 512) {\n                checksum += block[j];\n            }\n        }\n\n        Thread.sleep(100);\n        System.out.println("MemoryHog OK mb=" + megabytes + " checksum=" + checksum);\n    }\n}' },
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
    { label: 'fibonacci', source: 'function fib(n) {\n  if (n <= 1) return n;\n  return fib(n-1) + fib(n-2);\n}\nfor (let i = 0; i < 10; i++) {\n  console.log(fib(i));\n}' },
    { label: '[bad] infinite loop', source: 'while(true) {}' },
    { label: '[bad] memory bomb', source: 'const arr = [];\nwhile (true) {\n  arr.push("x".repeat(1024*1024));\n}' },
  ],
  verilog: [
    { label: 'hello world', source: 'module hello;\ninitial begin\n  $display("hello world");\n  $finish;\nend\nendmodule', stdin: '', expected: 'hello world\n' },
    { label: 'counter', source: 'module counter;\nreg [3:0] count;\ninitial begin\n  for (count = 0; count < 10; count = count + 1)\n    $display("count = %d", count);\n  $finish;\nend\nendmodule' },
  ],
  rust: [
    { label: 'hello world', source: 'fn main() {\n  println!("hello world");\n}', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'fn fib(n: u64) -> u64 {\n  match n {\n    0 | 1 => n,\n    _ => fib(n-1) + fib(n-2),\n  }\n}\nfn main() {\n  for i in 0..10 {\n    println!("{}", fib(i));\n  }\n}' },
    { label: 'fizzbuzz', source: 'fn main() {\n  for i in 1..=15 {\n    if i % 15 == 0 { println!("FizzBuzz"); }\n    else if i % 3 == 0 { println!("Fizz"); }\n    else if i % 5 == 0 { println!("Buzz"); }\n    else { println!("{}", i); }\n  }\n}' },
    { label: '[bad] panic', source: 'fn main() {\n  panic!("crash");\n}' },
    { label: '[bad] infinite loop', source: 'fn main() {\n  loop {}' },
    { label: '[bad] stack overflow', source: 'fn f() { f() }\nfn main() { f() }' },
  ],
  go: [
    { label: 'hello world', source: 'package main\nimport "fmt"\nfunc main() {\n  fmt.Println("hello world")\n}', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'package main\nimport "fmt"\nfunc fib(n int) int {\n  if n <= 1 { return n }\n  return fib(n-1) + fib(n-2)\n}\nfunc main() {\n  for i := 0; i < 10; i++ {\n    fmt.Println(fib(i))\n  }\n}' },
    { label: 'fizzbuzz', source: 'package main\nimport "fmt"\nfunc main() {\n  for i := 1; i <= 15; i++ {\n    if i%15 == 0 { fmt.Println("FizzBuzz") }\n    else if i%3 == 0 { fmt.Println("Fizz") }\n    else if i%5 == 0 { fmt.Println("Buzz") }\n    else { fmt.Println(i) }\n  }\n}' },
    { label: '[bad] nil deref', source: 'package main\nfunc main() {\n  var p *int\n  *p = 42\n}' },
    { label: '[bad] infinite loop', source: 'package main\nfunc main() {\n  for {}' },
  ],
  haskell: [
    { label: 'hello world', source: 'main = putStrLn "hello world"', stdin: '', expected: 'Hello from Haskell!\n' },
    { label: 'fibonacci', source: 'fib 0 = 0\nfib 1 = 1\nfib n = fib (n-1) + fib (n-2)\nmain = mapM_ (print . fib) [0..9]' },
    { label: '[bad] infinite loop', source: 'main = forever $ putStrLn "looping"' },
  ],
  ocaml: [
    { label: 'hello world', source: 'print_string "hello world\\n"', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'let rec fib n =\n  if n <= 1 then n\n  else fib (n-1) + fib (n-2)\nlet () =\n  for i = 0 to 9 do\n    print_int (fib i);\n    print_newline ()\n  done' },
    { label: '[bad] division by zero', source: 'let () =\n  let x = 1 / 0 in\n  print_int x' },
  ],
  r: [
    { label: 'hello world', source: 'cat("hello world\\n")', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'fib <- function(n) {\n  if (n <= 1) return(n)\n  fib(n-1) + fib(n-2)\n}\nfor (i in 0:9) {\n  cat(fib(i), "\\n")\n}' },
    { label: 'mean', source: 'numbers <- c(1, 2, 3, 4, 5)\ncat("mean:", mean(numbers), "\\n")\ncat("sum:", sum(numbers), "\\n")' },
    { label: '[bad] crash', source: 'this will crash' },
  ],
  d: [
    { label: 'hello world', source: 'import std.stdio;\nvoid main() {\n  writeln("hello world");\n}', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'import std.stdio;\nint fib(int n) {\n  if (n <= 1) return n;\n  return fib(n-1) + fib(n-2);\n}\nvoid main() {\n  foreach (i; 0..10)\n    writeln(fib(i));\n}' },
    { label: '[bad] null deref', source: 'import std.stdio;\nvoid main() {\n  int* p = null;\n  *p = 42;\n}' },
  ],
  lua: [
    { label: 'hello world', source: 'print("hello world")', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'function fib(n)\n  if n <= 1 then return n end\n  return fib(n-1) + fib(n-2)\nend\nfor i = 0, 9 do\n  print(fib(i))\nend' },
    { label: '[bad] infinite loop', source: 'while true do end' },
    { label: '[bad] crash', source: 'this will crash' },
  ],
  perl: [
    { label: 'hello world', source: 'print "hello world\\n"', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: 'sub fib {\n  my $n = shift;\n  return $n if $n <= 1;\n  fib($n-1) + fib($n-2);\n}\nforeach my $i (0..9) {\n  print fib($i) . "\\n";\n}' },
    { label: '[bad] crash', source: 'this will crash' },
  ],
  erl: [
    { label: 'hello world', source: '-module(solution).\n-export([start/0]).\nstart() ->\n  io:format("hello world~n").', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: '-module(solution).\n-export([start/0]).\n\nfib(0) -> 0;\nfib(1) -> 1;\nfib(N) -> fib(N-1) + fib(N-2).\n\nstart() ->\n  lists:foreach(\n    fun(I) -> io:format("~p~n", [fib(I)]) end,\n    lists:seq(0, 9)\n  ).' },
    { label: '[bad] crash', source: '-module(solution).\n-export([start/0]).\nstart() ->\n  1 / 0.' },
  ],
  lisp: [
    { label: 'hello world', source: '(format t "hello world~%")', stdin: '', expected: 'hello world\n' },
    { label: 'fibonacci', source: '(defun fib (n)\n  (if (<= n 1)\n      n\n      (+ (fib (- n 1)) (fib (- n 2)))))\n\n(dotimes (i 10)\n  (format t "~a~%" (fib i)))' },
    { label: '[bad] crash', source: '(error "this is a crash")' },
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
  const [lang, setLang] = useState('bash');
  const [code, setCode] = useState(LANGUAGES['bash'].defaultCode);
  const [stdin, setStdin] = useState('');
  const [output, setOutput] = useState('');
  const [rawJson, setRawJson] = useState<any>(null);
  const [showRaw, setShowRaw] = useState(false);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState<'play' | 'bench' | 'tests'>('play');
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
    setRawJson(null);
    setShowRaw(false);
    const body: any = { language: lang, source: code, tests: [{ stdin: stdin || '', expected_stdout: '' }] };
    try {
      const res = await fetch(API + '/run', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      const data = await res.json();
      setRawJson(data);
      if (data.error) { setOutput('error: ' + data.error.message); }
      else {
        let out = 'status: ' + data.status + '\n';
        if (data.build) out += 'build: ' + data.build.status + ' (' + data.build.duration_ms + 'ms)\n';
        if (data.tests && data.tests.length > 0) {
          const t = data.tests[0];
          out += 'test status: ' + t.status + '\n';
          out += '---stdout---\n' + (t.stdout || '(empty)') + '\n';
          out += '---stderr---\n' + (t.stderr || '(empty)') + '\n';
          if (t.memory_peak_kb > 0) out += 'memory: ' + t.memory_peak_kb + ' KB\n';
          out += 'duration: ' + t.duration_ms + 'ms\n';
          if (t.status === 'runtime_error' && !t.stderr) {
            out += 'note: crash signal may not appear in stderr (kernel kills silently)\n';
            out += 'try running locally or check exit code\n';
          }
        }
        if (data.build && data.build.status === 'ok' && (lang === 'c' || lang === 'cpp' || lang === 'rust' || lang === 'go')) {
          out += '\n(hint: compiled artifacts exist in the container but download is not yet implemented)\n';
        }
        setOutput(out);
      }
    } catch (e: any) { setOutput('request failed: ' + e.message); }
    setLoading(false);
  }

  const flatPresets = flattenPresets(PRESETS, lang);

  // --- Test runner state ---
  const [testcases, setTestcases] = useState<any[]>([]);
  const [selectedTc, setSelectedTc] = useState<any>(null);
  const [testResult, setTestResult] = useState<any>(null);
  const [testOutput, setTestOutput] = useState('');
  const [testLoading, setTestLoading] = useState(false);
  const [tcFilter, setTcFilter] = useState('');

  if (page === 'tests') {
    if (testcases.length === 0) {
      fetch(API + '/testcases').then(r => r.json()).then(d => setTestcases(d)).catch(() => {});
    }

    async function runTestcase(tc: any) {
      setTestLoading(true);
      setTestOutput('');
      setTestResult(null);
      try {
        const res = await fetch(API + '/run', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(tc.input) });
        const data = await res.json();
        const want = tc.want;
        setTestResult({ got: data, want });
        let out = '';
        out += '=== RESULT ===\n';
        const statusMatch = data.status === want.status;
        out += (statusMatch ? 'PASS' : 'FAIL') + ' status: got="' + data.status + '" want="' + want.status + '"\n';
        if (data.tests && want.tests) {
          data.tests.forEach((t: any, i: number) => {
            const w = want.tests[i] || {};
            const sMatch = t.status === w.status;
            out += (sMatch ? '  PASS' : '  FAIL') + ' test[' + i + '].status: got="' + t.status + '" want="' + w.status + '"\n';
            if (w.stdout && t.stdout !== w.stdout) {
              out += '  INFO test[' + i + '].stdout diff (expected length ' + w.stdout.length + ')\n';
            }
            if (w.stderr && t.stderr && !t.stderr.includes(w.stderr)) {
              out += '  INFO test[' + i + '].stderr diff\n';
            }
          });
        }
        if (data.build) {
          out += 'build: ' + data.build.status + ' (' + data.build.duration_ms + 'ms)\n';
        }
        if (data.tests && data.tests[0]) {
          const t = data.tests[0];
          out += 'duration: ' + t.duration_ms + 'ms\n';
          if (t.memory_peak_kb > 0) out += 'memory: ' + t.memory_peak_kb + ' KB\n';
          out += '---stdout---\n' + (t.stdout || '(empty)') + '\n';
          out += '---stderr---\n' + (t.stderr || '(empty)') + '\n';
        }
        setTestOutput(out);
      } catch (e: any) { setTestOutput('request failed: ' + e.message); }
      setTestLoading(false);
    }

    const filtered = testcases.filter((tc: any) =>
      tc.lang.toLowerCase().includes(tcFilter.toLowerCase()) ||
      tc.name.toLowerCase().includes(tcFilter.toLowerCase())
    );
    const grouped: Record<string, any[]> = {};
    filtered.forEach((tc: any) => {
      if (!grouped[tc.lang]) grouped[tc.lang] = [];
      grouped[tc.lang].push(tc);
    });
    const sortedLangs = Object.keys(grouped).sort();

    return React.createElement('div', { style: { fontFamily: 'monospace', padding: '10px', maxWidth: '1600px', margin: '0 auto' } },
      React.createElement('div', { style: { display: 'flex', gap: '20px', alignItems: 'center', marginBottom: '10px' } },
        React.createElement('h1', { style: { margin: 0 } }, 'test runner'),
        React.createElement('button', { onClick: () => setPage('play'), style: { cursor: 'pointer', fontSize: '14px' } }, 'back to playground'),
        React.createElement('span', { style: { fontSize: '12px', color: '#888' } }, testcases.length + ' testcases loaded'),
      ),
      React.createElement('input', {
        type: 'text',
        placeholder: 'filter tests...',
        value: tcFilter,
        onChange: (e: any) => setTcFilter(e.target.value),
        style: { width: '100%', padding: '6px', fontSize: '14px', marginBottom: '10px', boxSizing: 'border-box' },
      }),
      React.createElement('div', { style: { display: 'flex', gap: '10px', height: 'calc(100vh - 120px)' } },
        // Left panel: testcase list
        React.createElement('div', { style: { flex: '0 0 350px', overflow: 'auto', border: '1px solid #ccc', borderRadius: '4px', padding: '8px' } },
          sortedLangs.map(lang =>
            React.createElement('div', { key: lang, style: { marginBottom: '8px' } },
              React.createElement('div', { style: { fontWeight: 'bold', fontSize: '13px', marginBottom: '4px', color: '#555' } }, lang + ' (' + grouped[lang].length + ')'),
              grouped[lang].map((tc: any) =>
                React.createElement('div', {
                  key: tc.lang + '/' + tc.name,
                  onClick: () => { setSelectedTc(tc); setTestResult(null); setTestOutput(''); },
                  style: {
                    padding: '3px 8px', cursor: 'pointer', fontSize: '12px', borderRadius: '3px',
                    background: selectedTc && selectedTc.lang === tc.lang && selectedTc.name === tc.name ? '#e3f2fd' : 'transparent',
                  }
                },
                  (tc.name.startsWith('penetration-') ? '🛡️ ' : '🧪 ') + tc.name
                )
              )
            )
          ),
          testcases.length === 0 && React.createElement('div', { style: { color: '#888', fontSize: '13px' } }, 'loading testcases...'),
        ),
        // Right panel: testcase detail + run
        React.createElement('div', { style: { flex: 1, display: 'flex', flexDirection: 'column', gap: '10px', overflow: 'auto' } },
          !selectedTc && React.createElement('div', { style: { color: '#888', fontSize: '14px', padding: '20px' } }, 'select a testcase from the left panel'),
          selectedTc && React.createElement(React.Fragment, null,
            React.createElement('div', { style: { display: 'flex', gap: '10px', alignItems: 'center' } },
              React.createElement('h2', { style: { margin: 0, fontSize: '16px' } }, selectedTc.lang + ' / ' + selectedTc.name),
              React.createElement('button', {
                onClick: () => runTestcase(selectedTc),
                disabled: testLoading,
                style: { padding: '6px 16px', fontSize: '14px', cursor: testLoading ? 'wait' : 'pointer', background: testLoading ? '#ccc' : '#2196F3', color: 'white', border: 'none', borderRadius: '4px' }
              }, testLoading ? 'running...' : 'run test'),
            ),
            React.createElement('div', { style: { display: 'flex', gap: '10px', flex: 1 } },
              React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '12px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px' } },
                React.createElement('div', { style: { color: '#888', marginBottom: '4px', fontSize: '11px' } }, 'INPUT'),
                JSON.stringify(selectedTc.input, null, 2),
              ),
              React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '12px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px' } },
                React.createElement('div', { style: { color: '#888', marginBottom: '4px', fontSize: '11px' } }, 'EXPECTED'),
                JSON.stringify(selectedTc.want, null, 2),
              ),
              React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '12px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px' } },
                React.createElement('div', { style: { color: '#888', marginBottom: '4px', fontSize: '11px' } }, 'RESULT'),
                testOutput || (testLoading ? 'running...' : 'click "run test" to execute'),
              ),
            ),
          ),
        ),
      ),
    );
  }

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
      React.createElement('a', { href: '#', onClick: (e: any) => { e.preventDefault(); setPage('tests'); }, style: { fontSize: '14px' } }, 'tests'),
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
        output && rawJson && React.createElement('button', {
          onClick: () => setShowRaw(!showRaw),
          style: { fontSize: '12px', cursor: 'pointer', padding: '2px 8px', background: '#333', color: '#ccc', border: '1px solid #555', borderRadius: '3px' }
        }, showRaw ? 'hide raw' : 'view raw'),
        React.createElement('div', { style: { flex: 1, background: '#1e1e1e', color: '#d4d4d4', padding: '10px', fontFamily: 'monospace', fontSize: '13px', whiteSpace: 'pre-wrap', overflow: 'auto', borderRadius: '4px', minHeight: '200px' } },
          showRaw && rawJson ? JSON.stringify(rawJson, null, 2) : (output || 'output will appear here')),
      ),
    ),
  );
}

export default App;
