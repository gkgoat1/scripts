gh repo list $1 --source -L 400 --json name | python3 -c "import sys,json;data=json.load(sys.stdin);print('\n'.join(f'{r[\"name\"]}' for r in data))" | xargs -n 1 sh $(dirname $0)/pull.sh $1
