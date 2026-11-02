#!/usr/bin/env python3
"""Split terminal interface for lab, with numbered commands and q to exit."""
import codecs
import curses
import errno
import fcntl
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import termios
import textwrap

REPO = Path(__file__).resolve().parents[2]
MENUS = {
    'main': [('1', 'Server status', ['status']), ('2', 'Recent server logs', ['logs']),
             ('3', 'Restart server', ['restart']), ('4', 'Start server', ['start']),
             ('5', 'Stop server', ['stop']), ('6', 'SSH into server', ['ssh']),
             ('7', 'Deploy current source', ['deploy']), ('8', 'Local collector', 'collector'),
             ('9', 'Collector setup', 'setup'), ('0', 'Public HTTPS / Funnel', 'funnel')],
    'collector': [('1', 'Run collector', ['collector']),
                  ('2', 'Rebuild/restart', ['collector', '--restart'])],
    'setup': [('1', 'Install Petra collector', ['setup', 'petra']),
              ('2', 'Install Babylon collector', ['setup', 'babylon'])],
    'funnel': [('1', 'Enable public HTTPS', ['funnel', 'on']),
               ('2', 'Show status / URL', ['funnel', 'status']),
               ('3', 'Disable public HTTPS', ['funnel', 'off'])],
}
ANSI = re.compile(r'\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[@-_]')


def output_lines(text, width):
    lines = []
    for line in ANSI.sub('', text).expandtabs(4).replace('\r\n', '\n').split('\n'):
        line = ''.join(c for c in line.split('\r')[-1] if c >= ' ' and c != '\x7f')
        lines.extend(textwrap.wrap(line, max(1, width), replace_whitespace=False,
                                   drop_whitespace=False) or [''])
    return lines


def child_terminal(slave):
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)


class Menu:
    def __init__(self, screen):
        self.screen = screen
        self.section = 'main'
        self.output = 'Choose a command. Its output stays here until the next execution.'
        self.status = 'Ready'
        self.process = None
        self.master = None
        self.decoder = None
        self.eof = False
        screen.timeout(80)
        curses.raw()
        try:
            curses.curs_set(0)
        except curses.error:
            pass

    def text(self, y, x, value, width, style=0):
        if width > 0:
            try:
                self.screen.addnstr(y, x, value, width, style)
            except curses.error:
                pass

    def resize_child(self):
        if self.master is not None:
            rows, cols = self.screen.getmaxyx()
            fcntl.ioctl(self.master, termios.TIOCSWINSZ,
                        struct.pack('HHHH', max(1, rows - 6), max(1, cols - 36), 0, 0))

    def draw(self):
        self.screen.erase()
        rows, cols = self.screen.getmaxyx()
        if rows < 20 or cols < 76:
            self.text(0, 0, 'Resize terminal to at least 76 columns x 20 rows. q quits when idle.', cols - 1)
            self.screen.refresh()
            return
        split = 32
        self.text(0, 1, 'HOMELAB', 30, curses.A_BOLD)
        self.text(1, 1, 'Server: ' + os.environ.get('RMM_DEPLOY_TARGET', 'artemis'), cols - 2)
        self.screen.vline(3, split, curses.ACS_VLINE, rows - 5)
        self.text(3, 1, 'Commands' if self.section == 'main' else self.section.capitalize(), split - 2, curses.A_BOLD)
        for y, (key, label, _) in enumerate(MENUS[self.section], 5):
            self.text(y, 1, f'{key}) {label}', split - 2)
        self.text(17, 1, 'q) Quit' if self.section == 'main' else 'q) Back', split - 2)
        self.text(3, split + 2, self.status, cols - split - 3, curses.A_BOLD)
        lines = output_lines(self.output, cols - split - 4)
        for y, line in enumerate(lines[-(rows - 7):], 5):
            self.text(y, split + 2, line, cols - split - 3)
        hint = ('Running: typing goes to command; Ctrl+C interrupts.' if self.process
                else 'Press a number to select; q goes back or quits.')
        self.text(rows - 1, 1, hint, cols - 2)
        self.screen.refresh()

    def start(self, args):
        self.output = '$ ./lab ' + ' '.join(args) + '\n\n'
        self.status = 'Running: ' + ' '.join(args)
        command = ['bash', str(REPO / 'lab'), *args]
        if args[0] in ('ssh', 'collector'):
            curses.def_prog_mode()
            curses.endwin()
            try:
                result = subprocess.run(command, cwd=REPO).returncode
            finally:
                curses.reset_prog_mode()
                curses.raw()
                self.screen.clearok(True)
            self.output += 'Interactive session returned to Homelab.\n'
            self.status = f'Finished (exit {result})'
            return
        master, slave = pty.openpty()
        try:
            env = dict(os.environ, TERM='dumb', NO_COLOR='1')
            self.process = subprocess.Popen(command, cwd=REPO, env=env, stdin=slave,
                                            stdout=slave, stderr=slave,
                                            preexec_fn=lambda: child_terminal(slave))
        except BaseException:
            os.close(master)
            raise
        finally:
            os.close(slave)
        self.master = master
        self.decoder = codecs.getincrementaldecoder('utf-8')(errors='replace')
        self.eof = False
        self.resize_child()

    def collect(self):
        if not self.process:
            return
        for _ in range(16):
            if self.eof or not select.select([self.master], [], [], 0)[0]:
                break
            try:
                data = os.read(self.master, 8192)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b''
            if not data:
                self.eof = True
                self.output += self.decoder.decode(b'', final=True)
                break
            self.output = (self.output + self.decoder.decode(data))[-200000:]
        result = self.process.poll()
        if result is not None and self.eof:
            os.close(self.master)
            self.master = None
            self.process = None
            self.status = f'Finished (exit {result})'

    def close(self):
        if self.process:
            try:
                os.killpg(self.process.pid, signal.SIGTERM)
                self.process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait()
            except ProcessLookupError:
                pass
        if self.master is not None:
            os.close(self.master)

    def run(self):
        try:
            while True:
                self.collect()
                self.draw()
                key = self.screen.getch()
                if key == curses.KEY_RESIZE:
                    self.resize_child()
                elif self.process:
                    if 0 <= key < 256:
                        try:
                            os.write(self.master, bytes([key]))
                        except OSError as error:
                            if error.errno != errno.EIO:
                                raise
                elif key in (ord('q'), ord('Q')):
                    if self.section == 'main':
                        return
                    self.section = 'main'
                elif key >= 0:
                    for shortcut, _, action in MENUS[self.section]:
                        if key == ord(shortcut):
                            if isinstance(action, str):
                                self.section = action
                            else:
                                self.start(action)
                            break
        finally:
            self.close()


def main(screen):
    Menu(screen).run()


if __name__ == '__main__':
    curses.wrapper(main)
