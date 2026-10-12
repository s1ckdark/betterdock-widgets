#!/bin/sh
# 부팅 후 지난 시간과 1분 평균 부하를 JSON으로 출력한다.
# 앱은 PATH=/usr/bin:/bin만 주므로 /usr/sbin의 sysctl은 절대 경로로 부른다.
# 위젯 폴더에는 아무것도 쓰지 않는다(쓰면 승인 지문이 바뀐다).
set -eu

boot=$(/usr/sbin/sysctl -n kern.boottime | /usr/bin/sed -E 's/^\{ sec = ([0-9]+),.*/\1/')
now=$(/bin/date +%s)
elapsed=$((now - boot))
days=$((elapsed / 86400))
hours=$((elapsed % 86400 / 3600))
minutes=$((elapsed % 3600 / 60))
load1=$(/usr/sbin/sysctl -n vm.loadavg | /usr/bin/awk '{ print $2 }')

if [ "$days" -gt 0 ]; then
  label="${days}일 ${hours}시간"
elif [ "$hours" -gt 0 ]; then
  label="${hours}시간 ${minutes}분"
else
  label="${minutes}분"
fi

printf '{"label":"%s","days":%d,"hours":%d,"minutes":%d,"load1":"%s"}\n' \
  "$label" "$days" "$hours" "$minutes" "$load1"
