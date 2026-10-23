FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    gcc \
    g++ \
    make \
    python3 \
    python3-pip \
    git \
    wget \
    cpio \
    unzip \
    bc \
    rsync \
    xterm \
    autoconf \
    libtool \
    texinfo \
    zlib1g-dev \
    libssl-dev \
    libglib2.0-dev \
    libpixman-1-dev \
    libncurses5-dev \
    libncursesw5-dev \
    ncurses-dev \
    gcc-multilib \
    lsb-release \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m petalinux
USER petalinux
WORKDIR /home/petalinux
