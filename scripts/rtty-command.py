#!/usr/bin/python3
# JA_RTTY_UNIFIED_COMMAND_V1
"""Select the file helper for this controlling terminal, preserve legacy startup."""
import array
import fcntl
import os
import sys

LEGACY = '/usr/local/libexec/ja-rtty/rtty-legacy'
MODERN = '/usr/local/bin/rtty-tenant-poc'


def current_terminal_is_modern():
    with open('/dev/tty', 'rb', buffering=0) as tty:
        device = array.array('I', [0])
        fcntl.ioctl(tty.fileno(), 0x80045432, device, True)  # Linux TIOCGDEV
    address = '@rtty.file.tty.' + str(device[0])
    with open('/proc/net/unix', encoding='ascii') as sockets:
        return any(line.split()[-1:] == [address] for line in sockets)


def main():
    transfer = any(arg in ('-S', '-R') for arg in sys.argv[1:])
    executable = LEGACY if os.path.isfile(LEGACY) else MODERN
    if transfer:
        try:
            modern = current_terminal_is_modern()
        except OSError as error:
            sys.exit('无法识别当前终端，请在 rttys 终端中执行文件传输：' + str(error))
        executable = MODERN if modern else LEGACY
    if not os.path.isfile(executable) or not os.access(executable, os.X_OK):
        sys.exit('当前终端缺少匹配的 rtty 文件助手，请重新安装客户端包。')
    os.execv(executable, [executable] + sys.argv[1:])


if __name__ == '__main__':
    main()
