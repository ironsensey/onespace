#!/bin/sh
# User-observed working baseline. Do not change frame queue, bitrate, transport,
# sensor configuration or firmware as part of installing the OneSpace hub.
set -eu
exec gst-launch-1.0 -e \
  libcamerasrc \
  ! 'video/x-raw,width=1344,height=1008,format=NV12,framerate=46/1' \
  ! queue max-size-buffers=2 max-size-bytes=0 max-size-time=0 leaky=downstream \
  ! v4l2h264enc extra-controls="controls,video_bitrate=5000000,repeat_sequence_header=1" \
  ! 'video/x-h264,level=(string)4,profile=(string)baseline' \
  ! h264parse config-interval=-1 \
  ! rtspclientsink location=rtsp://127.0.0.1:8554/camera protocols=tcp
