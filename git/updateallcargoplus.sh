set -eux
for i in $(seq 1 20); do
sh $(dirname $0)/updateallcargo.sh
sh $(dirname $0)/pushall.sh
done
