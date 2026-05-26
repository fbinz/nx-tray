all: nx-tray

nx-tray: main.go nx-icon.png
	go build -o nx-tray .

install: nx-tray
	./install.sh

clean:
	rm -f nx-tray

.PHONY: all install clean
