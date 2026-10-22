"""Exercise hidden password entry in an isolated server administrator console."""
import os
import sys
import threading


def main():
    binary, database, username = sys.argv[1:]
    password = sys.stdin.readline().rstrip("\r\n")
    assert password, "Supply the isolated fixture password on stdin"
    arguments = [binary, "admin", "init", "--data", database, "--username", username]
    if os.name == "nt":
        from winpty import PtyProcess
        process = PtyProcess.spawn(arguments, dimensions=(30, 160))
        read = process.read
        write = process.write
        close = lambda: process.close(force=True)
    else:
        import pexpect
        process = pexpect.spawn(binary, arguments[1:], encoding="utf-8", timeout=20)
        read = process.read_nonblocking
        write = process.send
        close = lambda: process.close(force=True)
    timer = threading.Timer(25, close)
    timer.start()
    transcript = ""
    try:
        for prompt in ["Password (12+ characters):", "Confirm password:"]:
            while prompt not in transcript:
                transcript += read(1024)
            write(password + "\r\n")
        while "Administrator operation completed." not in transcript:
            transcript += read(1024)
        assert password not in transcript, "Password was echoed by the terminal"
        print("Administrator initialized through hidden terminal password entry.")
    finally:
        timer.cancel()
        close()


if __name__ == "__main__":
    main()
